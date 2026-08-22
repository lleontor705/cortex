import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Secret-storage canary: scans every non-test source file under web/src and
// fails when any forbidden persistence surface (or secret localStorage key)
// appears. Test files are excluded: they legitimately reference the secret
// key names while asserting their absence.
// This test file lives in web/src/lib, so one level up is web/src.
const srcRoot = join(dirname(fileURLToPath(import.meta.url)), "..");

function listSourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((entry) => {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      return listSourceFiles(full);
    }
    return /\.(ts|tsx)$/.test(entry) && !/\.test\.(ts|tsx)$/.test(entry)
      ? [full]
      : [];
  });
}

const FORBIDDEN: Array<{ label: string; pattern: RegExp }> = [
  { label: "sessionStorage persistence", pattern: /sessionStorage/ },
  { label: "cookie persistence", pattern: /document\s*\.\s*cookie/ },
  {
    label: "secret localStorage key access (cortex_token / cortex_llm_key)",
    pattern:
      /localStorage\s*\.\s*(getItem|setItem|removeItem)\s*\(\s*["']cortex_(token|llm_key)["']/,
  },
  { label: "IndexedDB persistence", pattern: /indexedDB/ },
  { label: "service worker registration", pattern: /navigator\s*\.\s*serviceWorker/ },
];

describe("web client secret-storage canary", () => {
  it("scans a non-empty set of source files", () => {
    const files = listSourceFiles(srcRoot);
    expect(files.length).toBeGreaterThan(5);
  });

  it("no source file uses a forbidden secret persistence surface", () => {
    const violations: string[] = [];
    for (const file of listSourceFiles(srcRoot)) {
      const source = readFileSync(file, "utf8");
      for (const { label, pattern } of FORBIDDEN) {
        if (pattern.test(source)) {
          violations.push(`${file}: ${label}`);
        }
      }
    }
    expect(violations).toEqual([]);
  });

  it("auth context never reads persisted tokens to auto-reconnect", () => {
    const source = readFileSync(join(srcRoot, "lib", "auth-context.tsx"), "utf8");
    expect(source).not.toMatch(/getItem\s*\(\s*["']cortex_token["']/);
    expect(source).not.toMatch(/setItem\s*\(\s*["']cortex_token["']/);
    expect(source).not.toMatch(/getItem\s*\(\s*["']cortex_llm_key["']/);
  });
});

describe("session-clearing canary", () => {
  it("auth context routes logout and 401 through one shared secret sweeper", () => {
    const source = readFileSync(join(srcRoot, "lib", "auth-context.tsx"), "utf8");
    // A single sweeper clears every live secret copy...
    expect(source).toMatch(/const clearLiveSecrets/);
    expect(source).toMatch(/setToken\(""\)/);
    expect(source).toMatch(/setLlmApiKey\(""\)/);
    // ...used by the logout path...
    expect(source).toMatch(/client\?\.\s*invalidate\(\);\s*clearLiveSecrets\(\);/);
    // ...and by the 401 invalidation callback.
    const callbackMatch = source.match(/new CortexClient\([^)]*\(\)\s*=>\s*\{([\s\S]*?)\n\s*\}\)/);
    expect(callbackMatch).not.toBeNull();
    expect(callbackMatch?.[1]).toMatch(/clearLiveSecrets\(\)/);
  });

  it("auth context fails the login handshake on 401 and never resurrects state", () => {
    const source = readFileSync(join(srcRoot, "lib", "auth-context.tsx"), "utf8");
    // The handshake is delegated to the tested pure module, which treats
    // 401 as terminal...
    expect(source).toMatch(/runLoginHandshake\(cli\)/);
    // ...connected state is only written after an ok, non-invalidated result.
    expect(source).toMatch(/if \(!result\.ok|result\.ok === false|invalidated\)/);
    expect(source).toMatch(/setIsConnected\(true\)/);
  });

  it("auth context rejects insecure destinations before creating a client", () => {
    const source = readFileSync(join(srcRoot, "lib", "auth-context.tsx"), "utf8");
    expect(source).toMatch(/validateBearerDestination\(url\)/);
  });

  it("settings page consumes the auth reset generation for both secret inputs", () => {
    const source = readFileSync(join(srcRoot, "app", "settings", "page.tsx"), "utf8");
    // Wiring proof only: the clearing SEMANTICS (typed secrets wiped when
    // the generation advances, even with provider values already empty —
    // the initial-login 401 / unsaved-key case) are behaviorally covered
    // by the pure-policy suite in src/lib/form-secret-reset.test.ts, which
    // needs no DOM. This canary proves the page is wired to that policy
    // for BOTH secret inputs and did not regress to value mirrors.
    expect(source).toMatch(/resetGeneration/);
    expect(source).toMatch(/observeResetGeneration/);
    expect(source).toMatch(/SecretInputState/);
    // A value-keyed mirror is not a reset event: it never re-fires when
    // the provider value was already empty, so it must not come back.
    expect(source).not.toMatch(/if \(token === ""\)\s*setInputToken\(""\)/);
    expect(source).not.toMatch(/if \(llmApiKey === ""\)\s*setInputLLMKey\(""\)/);
  });
});
