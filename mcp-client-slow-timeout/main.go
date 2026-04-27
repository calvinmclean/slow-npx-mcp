package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const timeout = 5 * time.Minute

type codeReceiver struct {
	authChan chan *auth.AuthorizationResult
	errChan  chan error
	server   *http.Server
}

func main() {
	if err := run(os.Args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	oauthEnabled := flags.Bool("oauth", true, "enable OAuth authorization-code flow on 401/403")
	callbackPort := flags.Int("callback-port", 3142, "localhost port for OAuth callback; use 0 for a random port")
	clientID := flags.String("client-id", "", "OAuth client ID for preregistered clients; dynamic registration is used when empty")
	clientSecret := flags.String("client-secret", "", "OAuth client secret for preregistered confidential clients")
	scope := flags.String("scope", "", "space-separated OAuth scopes to request")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		return fmt.Errorf("usage: %s [flags] <streamable-http-url>", args[0])
	}

	endpoint := flags.Arg(0)
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("URL must use http or https")
	}
	fmt.Fprintf(os.Stderr, "MCP server URL: %s\n", endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "mcp-list-tools",
		Version: "v0.1.0",
	}, nil)
	httpClient := &http.Client{Timeout: timeout}

	var receiver *codeReceiver
	var oauthHandler auth.OAuthHandler
	if *oauthEnabled {
		fmt.Fprintln(os.Stderr, "OAuth enabled; starting local callback listener")
		receiver, oauthHandler, err = newOAuthHandler(ctx, httpClient, *callbackPort, *clientID, *clientSecret, *scope)
		if err != nil {
			return err
		}
		defer receiver.close()
	} else {
		fmt.Fprintln(os.Stderr, "OAuth disabled")
	}

	fmt.Fprintln(os.Stderr, "Starting MCP connection attempt")
	connectStarted := time.Now()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           httpClient,
		OAuthHandler:         oauthHandler,
		DisableStandaloneSSE: true,
	}, nil)
	connectElapsed := time.Since(connectStarted)
	if err != nil {
		return fmt.Errorf("connect after %s: %w", connectElapsed.Round(time.Millisecond), err)
	}
	defer session.Close()
	fmt.Fprintf(os.Stderr, "Connected in %s; listing tools\n", connectElapsed.Round(time.Millisecond))

	listStarted := time.Now()
	count := 0
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("list tools after %s: %w", time.Since(listStarted).Round(time.Millisecond), err)
		}

		count++
		fmt.Println(tool.Name)
		if tool.Title != "" {
			fmt.Printf("  title: %s\n", tool.Title)
		}
		if description := strings.TrimSpace(tool.Description); description != "" {
			fmt.Printf("  description: %s\n", description)
		}
	}

	if count == 0 {
		fmt.Println("no tools")
	}
	fmt.Fprintf(os.Stderr, "Finished listing tools in %s: %d found\n", time.Since(listStarted).Round(time.Millisecond), count)

	return nil
}

func newOAuthHandler(ctx context.Context, client *http.Client, callbackPort int, clientID, clientSecret, scope string) (*codeReceiver, auth.OAuthHandler, error) {
	if clientSecret != "" && clientID == "" {
		return nil, nil, fmt.Errorf("-client-secret requires -client-id")
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", callbackPort))
	if err != nil {
		return nil, nil, fmt.Errorf("listen for OAuth callback: %w", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		return nil, nil, fmt.Errorf("read OAuth callback port: %w", err)
	}
	redirectURL := "http://localhost:" + port
	fmt.Fprintf(os.Stderr, "OAuth callback listening on %s\n", redirectURL)

	receiver := &codeReceiver{
		authChan: make(chan *auth.AuthorizationResult, 1),
		errChan:  make(chan error, 1),
	}
	go receiver.serve(listener)

	config := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              redirectURL,
		AuthorizationCodeFetcher: receiver.fetch,
		Client:                   client,
	}
	if clientID != "" {
		config.PreregisteredClient = &oauthex.ClientCredentials{ClientID: clientID}
		if clientSecret != "" {
			config.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: clientSecret}
		}
	} else {
		config.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:      "mcp-list-tools",
				RedirectURIs:    []string{redirectURL},
				SoftwareID:      "mcp-list-tools",
				SoftwareVersion: "v0.1.0",
				Scope:           scope,
			},
		}
	}

	handler, err := auth.NewAuthorizationCodeHandler(config)
	if err != nil {
		receiver.close()
		return nil, nil, fmt.Errorf("create OAuth handler: %w", err)
	}

	go func() {
		<-ctx.Done()
		receiver.close()
	}()

	return receiver, handler, nil
}

func (r *codeReceiver) serve(listener net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		result := &auth.AuthorizationResult{
			Code:  req.URL.Query().Get("code"),
			State: req.URL.Query().Get("state"),
		}
		if result.Code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			return
		}

		select {
		case r.authChan <- result:
		default:
		}
		fmt.Fprintln(w, "Authentication successful. You can close this window.")
	})

	r.server = &http.Server{Handler: mux}
	if err := r.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		select {
		case r.errChan <- err:
		default:
		}
	}
}

func (r *codeReceiver) fetch(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	fmt.Fprintf(os.Stderr, "Open this URL to authorize the MCP client:\n%s\n", args.URL)

	select {
	case result := <-r.authChan:
		fmt.Fprintln(os.Stderr, "OAuth authorization complete")
		return result, nil
	case err := <-r.errChan:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *codeReceiver) close() {
	if r.server != nil {
		_ = r.server.Close()
	}
}
