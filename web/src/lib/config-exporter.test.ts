import { describe, expect, it } from "vitest";
import {
  DEFAULT_TOKEN_ENV,
  generateClaudeDesktopConfig,
  generateCursorMcpConfig,
  generateWindsurfConfig,
  generateCortexYaml,
  generateEnvFile,
  generateQuickstartScript,
  remoteMcpUrl,
  validateBearerDestination,
  type AgentExportContext,
} from "./config-exporter";

const ctx: AgentExportContext = {
  serverUrl: "https://cortex.example/",
  projectName: "cortex",
};

// Any literal "Bearer <alnum-starting-value>" is a leaked secret. Legitimate
// outputs may only carry "Bearer ${ENV_VAR}" references or "<placeholders>".
const SECRET_CANARY = /Bearer\s+[A-Za-z0-9]/;

describe("remote MCP URL helper", () => {
  it("strips a trailing slash and appends /mcp", () => {
    expect(remoteMcpUrl("https://cortex.example/")).toBe("https://cortex.example/mcp");
    expect(remoteMcpUrl("https://cortex.example")).toBe("https://cortex.example/mcp");
    expect(remoteMcpUrl("http://localhost:7438/")).toBe("http://localhost:7438/mcp");
  });
});

describe("bearer destination transport policy", () => {
  it("accepts any HTTPS destination", () => {
    expect(() => validateBearerDestination("https://cortex.example")).not.toThrow();
    expect(() => validateBearerDestination("https://192.168.0.10:8443")).not.toThrow();
  });

  it("accepts plain HTTP only on strict loopback hosts", () => {
    expect(() => validateBearerDestination("http://localhost:7438")).not.toThrow();
    expect(() => validateBearerDestination("http://127.0.0.1:7438")).not.toThrow();
    expect(() => validateBearerDestination("http://127.0.0.2:7438")).not.toThrow();
    expect(() => validateBearerDestination("http://[::1]:7438")).not.toThrow();
  });

  it("rejects plain HTTP to non-loopback destinations", () => {
    expect(() => validateBearerDestination("http://cortex.example")).toThrow(/HTTPS/i);
    expect(() => validateBearerDestination("http://10.0.0.5:7438")).toThrow(/HTTPS/i);
    expect(() => validateBearerDestination("http://localhost.evil.example")).toThrow(/HTTPS/i);
    expect(() => validateBearerDestination("http://[::ffff:127.0.0.1]:7438")).toThrow(/HTTPS/i);
  });

  it("rejects non-HTTP(S) schemes and relative URLs", () => {
    expect(() => validateBearerDestination("file:///etc/passwd")).toThrow();
    expect(() => validateBearerDestination("/mcp")).toThrow();
  });
});

describe("agent configuration exporters", () => {
  it("generateClaudeDesktopConfig bridges the remote server via mcp-remote without secrets", () => {
    const parsed = JSON.parse(generateClaudeDesktopConfig(ctx)) as {
      mcpServers: {
        cortex: { command: string; args: string[]; env: Record<string, string> };
      };
    };
    const server = parsed.mcpServers.cortex;
    expect(server.command).toBe("npx");
    expect(server.args).toContain("mcp-remote");
    expect(server.args).toContain("https://cortex.example/mcp");
    // The token is referenced by env var name, never by value.
    expect(server.args.join(" ")).toContain(`Authorization:Bearer \${${DEFAULT_TOKEN_ENV}}`);
    // The child env must not define (or override) the token variable: the
    // bridge inherits it from the parent shell environment.
    expect(server.env).toEqual({});
    expect(JSON.stringify(parsed)).not.toMatch(SECRET_CANARY);
  });

  it("exported mcp-remote config resolves the token from the inherited shell environment", () => {
    for (const generate of [generateClaudeDesktopConfig, generateCursorMcpConfig, generateWindsurfConfig]) {
      const parsed = JSON.parse(generate(ctx)) as {
        mcpServers: {
          cortex: { args: string[]; env: Record<string, string> };
        };
      };
      const server = parsed.mcpServers.cortex;

      // Simulate the agent runner: the child env overlays the parent env.
      const parentEnv: Record<string, string> = {
        [DEFAULT_TOKEN_ENV]: "tok_from_shell",
        PATH: "/usr/bin",
      };
      const effectiveEnv: Record<string, string> = { ...parentEnv, ...server.env };

      // The declared env never overrides the shell-exported token...
      expect(effectiveEnv[DEFAULT_TOKEN_ENV]).toBe("tok_from_shell");
      // ...and the header argument interpolates to the inherited value.
      const headerArg = server.args.find((a) => a.startsWith("Authorization:"));
      if (headerArg === undefined) {
        throw new Error("exported config must reference the Authorization header");
      }
      const rendered = headerArg.replace(
        /\$\{([A-Za-z_][A-Za-z0-9_]*)\}/g,
        (_m, key: string) => effectiveEnv[key] ?? "",
      );
      expect(rendered).toBe("Authorization:Bearer tok_from_shell");
    }
  });

  it("generateCursorMcpConfig uses the supported stdio mcp-remote shape without secrets", () => {
    const parsed = JSON.parse(generateCursorMcpConfig(ctx)) as {
      mcpServers: {
        cortex: { command: string; args: string[]; env: Record<string, string> };
      };
    };
    const server = parsed.mcpServers.cortex;
    expect(server.args).toContain("mcp-remote");
    expect(server.args).toContain("https://cortex.example/mcp");
    expect(server.args.join(" ")).toContain(`Authorization:Bearer \${${DEFAULT_TOKEN_ENV}}`);
    expect(JSON.stringify(parsed)).not.toMatch(SECRET_CANARY);
  });

  it("generateWindsurfConfig uses the supported stdio mcp-remote shape without secrets", () => {
    const parsed = JSON.parse(generateWindsurfConfig(ctx)) as {
      mcpServers: {
        cortex: { command: string; args: string[]; env: Record<string, string> };
      };
    };
    const server = parsed.mcpServers.cortex;
    expect(server.args).toContain("mcp-remote");
    expect(server.args).toContain("https://cortex.example/mcp");
    expect(JSON.stringify(parsed)).not.toMatch(SECRET_CANARY);
  });

  it("generateCortexYaml matches the real mcp.remote schema and never embeds a token", () => {
    const yaml = generateCortexYaml({ ...ctx, projectName: undefined });
    expect(yaml).toContain("mcp:");
    expect(yaml).toContain("remote:");
    expect(yaml).toContain("enabled: true");
    expect(yaml).toContain('url: "https://cortex.example/mcp"');
    expect(yaml).toContain(`token_env: "${DEFAULT_TOKEN_ENV}"`);
    expect(yaml).toContain("timeout: 30s");
    expect(yaml).toContain("search:");
    // A secret-bearing `token:` key must not exist anywhere in the YAML.
    expect(yaml).not.toMatch(/^\s*token:/m);
    expect(yaml).not.toMatch(SECRET_CANARY);
  });

  it("generateEnvFile never assigns the token env var", () => {
    const env = generateEnvFile(ctx);
    expect(env.split("\n")).toEqual(
      expect.arrayContaining([
        "CORTEX_SERVER_URL=https://cortex.example",
        "CORTEX_PROJECT=cortex",
      ]),
    );
    // No active assignment: sourcing this file must never blank or override
    // a token exported by the user's shell.
    expect(env).not.toMatch(new RegExp(`^\\s*(export\\s+)?${DEFAULT_TOKEN_ENV}\\s*=`, "m"));
    expect(env).toContain(DEFAULT_TOKEN_ENV);
    expect(env).not.toMatch(SECRET_CANARY);
  });

  it("no generated artifact embeds or overrides the token env var", () => {
    const artifacts: Array<[string, string]> = [
      ["claude", generateClaudeDesktopConfig(ctx)],
      ["cursor", generateCursorMcpConfig(ctx)],
      ["windsurf", generateWindsurfConfig(ctx)],
      ["yaml", generateCortexYaml(ctx)],
      ["env", generateEnvFile(ctx)],
      ["sh", generateQuickstartScript(ctx, "sh")],
      ["ps1", generateQuickstartScript(ctx, "ps1")],
    ];
    for (const [label, artifact] of artifacts) {
      // No active shell/env-file assignment of the token variable...
      expect(artifact, `${label}: active token assignment`).not.toMatch(
        new RegExp(`^\\s*(export\\s+)?${DEFAULT_TOKEN_ENV}\\s*=`, "m"),
      );
      // ...and no JSON/YAML key that would set it as a literal value.
      expect(artifact, `${label}: token mapped as a value key`).not.toMatch(
        new RegExp(`["']${DEFAULT_TOKEN_ENV}["']\\s*:`),
      );
      expect(artifact, `${label}: secret canary`).not.toMatch(SECRET_CANARY);
    }
  });

  it("generateQuickstartScript reads the token from the environment, never embeds it", () => {
    const ps1 = generateQuickstartScript(ctx, "ps1");
    const sh = generateQuickstartScript(ctx, "sh");

    expect(ps1).toContain('$env:CORTEX_SERVER_URL = "https://cortex.example"');
    expect(ps1).toContain("CORTEX_REMOTE_TOKEN");
    expect(ps1).not.toContain("#!/usr/bin/env bash");
    expect(ps1).not.toMatch(SECRET_CANARY);

    expect(sh).toContain("#!/usr/bin/env bash");
    expect(sh).toContain('export CORTEX_SERVER_URL="https://cortex.example"');
    expect(sh).toContain("Bearer ${CORTEX_REMOTE_TOKEN}");
    expect(sh).not.toContain("$env:");
    expect(sh).not.toMatch(SECRET_CANARY);
  });

  it("every exporter refuses insecure non-loopback HTTP destinations", () => {
    const insecure: AgentExportContext = { serverUrl: "http://cortex.example" };
    expect(() => generateClaudeDesktopConfig(insecure)).toThrow();
    expect(() => generateCursorMcpConfig(insecure)).toThrow();
    expect(() => generateWindsurfConfig(insecure)).toThrow();
    expect(() => generateCortexYaml(insecure)).toThrow();
    expect(() => generateEnvFile(insecure)).toThrow();
    expect(() => generateQuickstartScript(insecure, "sh")).toThrow();
    expect(() => generateQuickstartScript(insecure, "ps1")).toThrow();
  });

  it("every exporter accepts strict loopback HTTP destinations", () => {
    const loopback: AgentExportContext = { serverUrl: "http://localhost:7438" };
    expect(() => generateClaudeDesktopConfig(loopback)).not.toThrow();
    expect(() => generateCortexYaml(loopback)).not.toThrow();
    expect(() => generateQuickstartScript(loopback, "sh")).not.toThrow();
  });
});
