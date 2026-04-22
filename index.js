#!/usr/bin/env node

const delaySeconds = parseInt(process.env.SLOW_MCP_DELAY || "60", 10);

setTimeout(() => {
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
