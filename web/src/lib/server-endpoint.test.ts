import { afterEach, describe, expect, it } from "vitest";
import {
  DEFAULT_SERVER_URL,
  resolveServerEndpoint,
  serverEndpoint,
} from "./server-endpoint";

describe("server endpoint build configuration", () => {
  it("keeps the local server URL configurable for a detached web deployment", () => {
    expect(resolveServerEndpoint({ url: "https://cortex.example/" })).toEqual({
      managed: false,
      url: "https://cortex.example",
    });
  });

  it("uses the Compose endpoint without asking the browser user", () => {
    expect(resolveServerEndpoint({ managed: "true", url: "http://localhost:7438" })).toEqual({
      managed: true,
      url: "http://localhost:7438",
    });
  });

  it("falls back to the local server when a managed build has no explicit URL", () => {
    expect(resolveServerEndpoint({ managed: " TRUE " })).toEqual({
      managed: true,
      url: DEFAULT_SERVER_URL,
    });
  });
});

describe("lazy runtime endpoint resolution", () => {
  const globalWithWindow = globalThis as { window?: unknown };

  afterEach(() => {
    delete globalWithWindow.window;
  });

  it("reads the injected runtime config at access time, not module load", () => {
    // The runtime /config.js script may execute after the async app bundles;
    // resolution must therefore observe the config whenever the UI first
    // renders, or a same-origin deployment degrades to the localhost default.
    globalWithWindow.window = {
      __CORTEX_WEB_CONFIG__: { serverUrl: "https://cortex.example" },
    };
    expect(serverEndpoint.url).toBe("https://cortex.example");
    expect(serverEndpoint.managed).toBe(true);
  });

  it("keeps the documented default when no runtime config exists", () => {
    delete globalWithWindow.window;
    expect(resolveServerEndpoint()).toEqual({ managed: false, url: DEFAULT_SERVER_URL });
  });
});
