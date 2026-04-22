#!/usr/bin/env node

const { McpServer } = require("@modelcontextprotocol/sdk/server/mcp.js");
const {
  StdioServerTransport,
} = require("@modelcontextprotocol/sdk/server/stdio.js");

const delaySeconds = parseInt(process.argv[2] || "60", 10);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function main() {
  for (let remaining = delaySeconds; remaining > 0; remaining--) {
    process.stderr.write(`slow-npx-mcp: ${remaining}s remaining...\n`);
    await sleep(1000);
  }

  process.stderr.write("slow-npx-mcp: starting server\n");

  const server = new McpServer({
    name: "slow-server",
    version: "1.0.0",
  });

  server.tool("ping", "returns pong", {}, async () => ({
    content: [{ type: "text", text: "pong" }],
  }));

  server.connect(new StdioServerTransport());
}

main();
