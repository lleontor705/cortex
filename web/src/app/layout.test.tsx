import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const layoutSource = readFileSync(new URL("./layout.tsx", import.meta.url), "utf8");

describe("Root layout runtime endpoint contract", () => {
  it("loads /config.js so the embedded server injects the same-origin endpoint", () => {
    // Without this tag the browser never receives window.__CORTEX_WEB_CONFIG__
    // and resolveServerEndpoint falls back to the localhost dev default, which
    // breaks the health poll and web-key probe on any non-loopback deployment.
    expect(layoutSource).toContain('<script src="/config.js" />');
  });

  it("renders the config script inside <head> ahead of the app body", () => {
    const headIndex = layoutSource.indexOf("<head>");
    const scriptIndex = layoutSource.indexOf('<script src="/config.js" />');
    const bodyIndex = layoutSource.indexOf("<body>");
    expect(headIndex).toBeGreaterThanOrEqual(0);
    expect(scriptIndex).toBeGreaterThan(headIndex);
    expect(scriptIndex).toBeLessThan(bodyIndex);
  });
});
