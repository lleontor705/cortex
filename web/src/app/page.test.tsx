import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const pageSource = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");

describe("Dashboard page contract", () => {
  it("queries client.stats to obtain real observation counts instead of truncating", () => {
    // Queries client.stats() during dashboard loading
    expect(pageSource).toContain("client.stats()");
    // Holds server stats in state
    expect(pageSource).toContain("dashboardStats");
    // Displays real total count from total_observations or observations
    expect(pageSource).toContain("currentStats?.total_observations");
    expect(pageSource).toContain("currentStats?.observations");
    // Uses safe fallback to recentObs.length
    expect(pageSource).toContain("recentObs.length");
  });

  it("passes totalObservations into the observations StatCard", () => {
    expect(pageSource).toContain("value={totalObservations}");
  });
});
