import { describe, expect, it } from "vitest";
import { DEFAULT_SERVER_URL, resolveServerEndpoint } from "./server-endpoint";

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
