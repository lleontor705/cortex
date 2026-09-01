import { defineConfig } from "vitest/config";

// Deterministic node-environment oracle for the web client's pure modules
// (API client, config exporters). Component/DOM tests would add jsdom; the
// current src tree has no components yet, so node is the honest scope.
export default defineConfig({
  test: {
    environment: "node",
    include: ["src/**/*.test.{ts,tsx}"],
    coverage: {
      provider: "v8",

      include: ["src/lib/**/*.ts"],
      exclude: ["src/**/*.test.ts", "src/**/*.test.tsx"],
      reporter: ["text", "json-summary", "lcov"],
      reportsDirectory: "coverage",
      thresholds: {
        statements: 70,
        branches: 60,
        functions: 55,
        lines: 70,
      },
    },
  },
});
