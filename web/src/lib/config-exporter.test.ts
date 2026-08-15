import { describe, expect, it } from "vitest";
import {
  generateClaudeDesktopConfig,
  generateCursorMcpConfig,
  generateCortexYaml,
  generateEnvFile,
  generateQuickstartScript,
  type AgentExportContext,
} from "./config-exporter";

const ctx: AgentExportContext = {
  serverUrl: "https://cortex.example/",
  token: "secret-token",
  projectName: "cortex",
};

describe("agent configuration exporters", () => {
  it("generateClaudeDesktopConfig emits valid MCP JSON with auth env", () => {
    const parsed = JSON.parse(generateClaudeDesktopConfig(ctx)) as {
      mcpServers: {
        cortex: {
          args: string[];
          env: Record<string, string>;
        };
      };
    };
    const server = parsed.mcpServers.cortex;
    // Trailing slash on the server URL must be stripped before appending /mcp.
    expect(server.args.at(-1)).toBe("https://cortex.example/mcp");
    expect(server.env.CORTEX_TOKEN).toBe("secret-token");
    expect(server.env.AUTHORIZATION).toBe("Bearer secret-token");
  });

  it("generateCursorMcpConfig points at the MCP endpoint with a bearer header", () => {
    const parsed = JSON.parse(generateCursorMcpConfig(ctx)) as {
      mcpServers: { cortex: { url: string; headers: Record<string, string> } };
    };
    const server = parsed.mcpServers.cortex;
    expect(server.url).toBe("https://cortex.example/mcp");
    expect(server.headers.Authorization).toBe("Bearer secret-token");
  });

  it("generateCortexYaml embeds server credentials and defaults the project", () => {
    const yaml = generateCortexYaml({ ...ctx, projectName: undefined });
    expect(yaml).toContain('project: "default"');
    expect(yaml).toContain('url: "https://cortex.example/"');
    expect(yaml).toContain('token: "secret-token"');
    expect(yaml).toContain('mode: "server"');
  });

  it("generateEnvFile renders the three connection variables", () => {
    const env = generateEnvFile(ctx);
    expect(env.split("\n")).toEqual(
      expect.arrayContaining([
        "CORTEX_SERVER_URL=https://cortex.example/",
        "CORTEX_TOKEN=secret-token",
        "CORTEX_PROJECT=cortex",
      ]),
    );
  });

  it("generateQuickstartScript branches per OS and never leaks the other shell", () => {
    const ps1 = generateQuickstartScript(ctx, "ps1");
    const sh = generateQuickstartScript(ctx, "sh");
    expect(ps1).toContain("$env:CORTEX_TOKEN = \"secret-token\"");
    expect(ps1).not.toContain("#!/usr/bin/env bash");
    expect(sh).toContain('export CORTEX_TOKEN="secret-token"');
    expect(sh).toContain("#!/usr/bin/env bash");
    expect(sh).not.toContain("$env:");
  });
});
