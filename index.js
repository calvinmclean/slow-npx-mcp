#!/usr/bin/env node

const delaySeconds = parseInt(process.argv[2] || "60", 10);

let remaining = delaySeconds;
process.stderr.write(`slow-npx-mcp: starting in ${remaining}s...\n`);

const countdown = setInterval(() => {
  remaining--;
  if (remaining > 0) {
    process.stderr.write(`slow-npx-mcp: ${remaining}s remaining...\n`);
  }
}, 1000);

setTimeout(() => {
  clearInterval(countdown);
  process.stderr.write("slow-npx-mcp: starting server\n");

  const { McpServer } = require("@modelcontextprotocol/sdk/server/mcp.js");
  const {
    StdioServerTransport,
  } = require("@modelcontextprotocol/sdk/server/stdio.js");

  const server = new McpServer({
    name: "slow-server",
    version: "1.0.0",
  });

  server.tool("ping", "returns pong", {}, async () => ({
    content: [{ type: "text", text: "pong" }],
  }));

  server.connect(new StdioServerTransport());
}, delaySeconds * 1000);
