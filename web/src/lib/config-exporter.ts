// Secret-free agent configuration exports.
//
// Exported files NEVER contain bearer tokens or LLM keys. Every export
// references the token through an environment variable name (token_env,
// matching the Cortex CLI's `mcp.remote.token_env` config key) that the
// user fills in outside the downloaded file.
//
// The transport policy mirrors internal/transportpolicy: HTTPS everywhere,
// plain HTTP only for strict loopback destinations.

import { validateBearerDestination } from "./transport-policy";

// Re-exported for existing consumers; the canonical home is
// ./transport-policy, shared with the API client.
export {
  validateBearerDestination,
  InsecureTransportError as ExportTransportError,
} from "./transport-policy";

export const DEFAULT_TOKEN_ENV = "CORTEX_REMOTE_TOKEN";

export interface AgentExportContext {
  serverUrl: string;
  /** Environment variable name that will hold the bearer token. */
  tokenEnv?: string;
  userEmail?: string;
  tokenName?: string;
  projectName?: string;
}

function assertDestination(ctx: AgentExportContext): void {
  validateBearerDestination(ctx.serverUrl);
}

function tokenEnvOf(ctx: AgentExportContext): string {
  return ctx.tokenEnv || DEFAULT_TOKEN_ENV;
}

/** Strips a trailing slash and appends the Streamable HTTP MCP path. */
export function remoteMcpUrl(serverUrl: string): string {
  return `${serverUrl.replace(/\/$/, "")}/mcp`;
}

interface McpRemoteServer {
  command: string;
  args: string[];
  env: Record<string, string>;
}

// `mcp-remote` is the supported stdio bridge for remote HTTP MCP servers in
// Claude Desktop, Cursor and Windsurf. The header value is resolved from the
// environment at runtime; the exported file only carries the variable NAME.
function mcpRemoteServer(ctx: AgentExportContext): McpRemoteServer {
  const tokenEnv = tokenEnvOf(ctx);
  return {
    command: "npx",
    args: [
      "-y",
      "mcp-remote",
      remoteMcpUrl(ctx.serverUrl),
      "--header",
      `Authorization:Bearer \${${tokenEnv}}`,
    ],
    // The child process must inherit ${tokenEnv} from the parent shell.
    // Declaring the variable here — even as an inert placeholder — would
    // overlay (and therefore override) the user's exported value, so env
    // stays empty by design.
    env: {},
  };
}

export function generateClaudeDesktopConfig(ctx: AgentExportContext): string {
  assertDestination(ctx);
  return JSON.stringify(
    { mcpServers: { cortex: mcpRemoteServer(ctx) } },
    null,
    2,
  );
}

export function generateCursorMcpConfig(ctx: AgentExportContext): string {
  assertDestination(ctx);
  return JSON.stringify(
    { mcpServers: { cortex: mcpRemoteServer(ctx) } },
    null,
    2,
  );
}

export function generateWindsurfConfig(ctx: AgentExportContext): string {
  assertDestination(ctx);
  return JSON.stringify(
    { mcpServers: { cortex: mcpRemoteServer(ctx) } },
    null,
    2,
  );
}

export function generateVSCodeClineConfig(ctx: AgentExportContext): string {
  assertDestination(ctx);
  return JSON.stringify(
    { mcpServers: { cortex: mcpRemoteServer(ctx) } },
    null,
    2,
  );
}

export function generateOpenCodeConfig(ctx: AgentExportContext): string {
  assertDestination(ctx);
  return JSON.stringify(
    {
      $schema: "https://opencode.ai/config.json",
      mcp: {
        cortex: {
          type: "remote",
          url: remoteMcpUrl(ctx.serverUrl),
          headers: {
            Authorization: `Bearer {env:${tokenEnvOf(ctx)}}`,
          },
        },
      },
    },
    null,
    2,
  );
}

export function generateCortexYaml(ctx: AgentExportContext): string {
  assertDestination(ctx);
  const tokenEnv = tokenEnvOf(ctx);
  return `# Cortex Client & Agent Configuration
# Cortex never stores tokens in this file. Export the token into your
# environment before starting the Cortex CLI:
#   export ${tokenEnv}="<paste-your-cortex-token>"     # sh/bash
#   $env:${tokenEnv} = "<paste-your-cortex-token>"     # PowerShell
mcp:
  remote:
    enabled: true
    url: "${remoteMcpUrl(ctx.serverUrl)}"
    token_env: "${tokenEnv}"
    timeout: 30s
search:
  default_limit: 20
`;
}

export function generateEnvFile(ctx: AgentExportContext): string {
  assertDestination(ctx);
  const tokenEnv = tokenEnvOf(ctx);
  return `# Cortex Environment Variables
# ${tokenEnv} is the bearer token for the remote MCP server.
# Set it in your shell session — never in this file, never committed:
#   export ${tokenEnv}="<paste-your-cortex-token>"     # sh/bash
#   $env:${tokenEnv} = "<paste-your-cortex-token>"     # PowerShell
CORTEX_SERVER_URL=${ctx.serverUrl.replace(/\/$/, "")}
CORTEX_PROJECT=${ctx.projectName || "default"}
`;
}

export function generateQuickstartScript(ctx: AgentExportContext, os: "sh" | "ps1"): string {
  assertDestination(ctx);
  const tokenEnv = tokenEnvOf(ctx);
  const serverUrl = ctx.serverUrl.replace(/\/$/, "");
  if (os === "ps1") {
    return `# Cortex Quickstart Connection Script (PowerShell)
# Set your token first: $env:${tokenEnv} = "<paste-your-cortex-token>"
$ErrorActionPreference = "Stop"
$env:CORTEX_SERVER_URL = "${serverUrl}"
if (-not $env:${tokenEnv}) {
  throw "${tokenEnv} must be set to your Cortex bearer token"
}
Write-Host "Connecting to Cortex Server at $($env:CORTEX_SERVER_URL)..." -ForegroundColor Cyan
Invoke-RestMethod -Uri "$($env:CORTEX_SERVER_URL)/health" -Method Get | Format-Table
Write-Host "Cortex Agent environment configured successfully!" -ForegroundColor Green
`;
  }
  return `#!/usr/bin/env bash
# Cortex Quickstart Connection Script (Bash)
# Set your token first: export ${tokenEnv}="<paste-your-cortex-token>"
set -euo pipefail
export CORTEX_SERVER_URL="${serverUrl}"
: "\${${tokenEnv}?:${tokenEnv} must be set to your Cortex bearer token}"
echo "Connecting to Cortex Server at \${CORTEX_SERVER_URL}..."
curl -s -H "Authorization: Bearer \${${tokenEnv}}" "\${CORTEX_SERVER_URL}/health"
echo ""
echo "Cortex Agent environment configured successfully!"
`;
}

export function downloadFile(filename: string, content: string, mimeType = "application/json") {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}
