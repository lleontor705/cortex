import { defineConfig } from "vitest/config";

// Deterministic node-environment oracle for the web client's pure modules
// (API client, config exporters). Component/DOM tests would add jsdom; the
// current src tree has no components yet, so node is the honest scope.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
