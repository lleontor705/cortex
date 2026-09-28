import { test, expect } from "@playwright/test";
import * as fs from "node:fs";

// `cortex serve` prints the minted `ctx_...` key to stdout exactly once on a
// first boot. The harness reads that console hand-off (the config tees serve
// stdout to CORTEX_E2E_SERVE_LOG); it never reads the at-rest web.key file.
const serveLog = process.env.CORTEX_E2E_SERVE_LOG ?? "";
const baseURL = process.env.CORTEX_E2E_BASE_URL ?? "";
const MINTED_KEY_PATTERN = /^(ctx_[A-Za-z0-9_-]+)$/m;

function readMintedKey(): string {
  const output = fs.readFileSync(serveLog, "utf8");
  const match = output.match(MINTED_KEY_PATTERN);
  if (!match) {
    throw new Error(`cortex e2e: no web access key printed in serve stdout:\n${output}`);
  }
  return match[1];
}

test.describe("first-boot web key onboarding", () => {
  test("mints once, clears the web-key gate, and never re-prompts after reload", async ({
    page,
  }) => {
    // The app defaults its non-secret API endpoint preference to the documented
    // localhost:7438 origin, so point it at the harness's run-derived origin
    // before the bundle loads. The once-printed web key stays the credential
    // under test; this only configures which server the UI talks to.
    await page.addInitScript((url) => {
      window.localStorage.setItem("cortex_server_url", url);
    }, baseURL);

    const webKey = readMintedKey();
    expect(webKey).toMatch(MINTED_KEY_PATTERN);

    await page.goto("/");

    const unlockHeading = page.getByRole("heading", { name: "Cortex Web Access" });
    await expect(unlockHeading).toBeVisible({ timeout: 30_000 });

    await page.getByLabel("WEB ACCESS KEY").fill(webKey);
    await page.getByRole("button", { name: "Unlock Cortex" }).click();
    await expect(unlockHeading).toBeHidden();

    // Clearing the web-key gate is the first-boot outcome. Reaching the full
    // navigation additionally requires a bearer token, so AppShell renders its
    // "Cortex Control Room" connect card until the session is isConnected.
    const controlRoomHeading = page.getByRole("heading", { name: "Cortex Control Room" });
    await expect(controlRoomHeading).toBeVisible();

    await page.reload();
    await expect(unlockHeading).toBeHidden();
    await expect(controlRoomHeading).toBeVisible();
  });
});
