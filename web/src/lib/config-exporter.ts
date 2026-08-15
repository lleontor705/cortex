export interface AgentExportContext {
  serverUrl: string;
  token: string;
  userEmail?: string;
  tokenName?: string;
  projectName?: string;
}

export function generateClaudeDesktopConfig(ctx: AgentExportContext): string {
  const mcpUrl = `${ctx.serverUrl.replace(/\/$/, "")}/mcp`;
  const config = {
    mcpServers: {
      cortex: {
        command: "npx",
        args: [
          "-y",
          "@modelcontextprotocol/server-fetch",
          mcpUrl,
        ],
        env: {
          CORTEX_SERVER_URL: ctx.serverUrl,
          CORTEX_TOKEN: ctx.token,
          AUTHORIZATION: `Bearer ${ctx.token}`,
        },
      },
    },
  };
  return JSON.stringify(config, null, 2);
}

export function generateCursorMcpConfig(ctx: AgentExportContext): string {
  const mcpUrl = `${ctx.serverUrl.replace(/\/$/, "")}/mcp`;
  const config = {
    mcpServers: {
      cortex: {
        url: mcpUrl,
        headers: {
          Authorization: `Bearer ${ctx.token}`,
        },
      },
    },
  };
  return JSON.stringify(config, null, 2);
}

export function generateWindsurfConfig(ctx: AgentExportContext): string {
  const mcpUrl = `${ctx.serverUrl.replace(/\/$/, "")}/mcp`;
  const config = {
    mcpServers: {
      cortex: {
        serverUrl: mcpUrl,
        headers: {
          Authorization: `Bearer ${ctx.token}`,
        },
      },
    },
  };
  return JSON.stringify(config, null, 2);
}

export function generateCortexYaml(ctx: AgentExportContext): string {
  return `# Cortex Client & Agent Configuration
version: "2"
project: "${ctx.projectName || "default"}"
server:
  url: "${ctx.serverUrl}"
  token: "${ctx.token}"
storage:
  mode: "server"
search:
  default_limit: 20
  vector_threshold: 0.7
`;
}

export function generateEnvFile(ctx: AgentExportContext): string {
  return `# Cortex Environment Variables
CORTEX_SERVER_URL=${ctx.serverUrl}
CORTEX_TOKEN=${ctx.token}
CORTEX_PROJECT=${ctx.projectName || "default"}
`;
}

export function generateQuickstartScript(ctx: AgentExportContext, os: "sh" | "ps1"): string {
  if (os === "ps1") {
    return `# Cortex Quickstart Connection Script (PowerShell)
$env:CORTEX_SERVER_URL = "${ctx.serverUrl}"
$env:CORTEX_TOKEN = "${ctx.token}"
Write-Host "Connecting to Cortex Server at ${ctx.serverUrl}..." -ForegroundColor Cyan
Invoke-RestMethod -Uri "${ctx.serverUrl}/health" -Method Get | Format-Table
Write-Host "Cortex Agent environment configured successfully!" -ForegroundColor Green
`;
  }
  return `#!/usr/bin/env bash
# Cortex Quickstart Connection Script (Bash)
export CORTEX_SERVER_URL="${ctx.serverUrl}"
export CORTEX_TOKEN="${ctx.token}"
echo "Connecting to Cortex Server at ${ctx.serverUrl}..."
curl -s -H "Authorization: Bearer ${ctx.token}" "${ctx.serverUrl}/health"
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
