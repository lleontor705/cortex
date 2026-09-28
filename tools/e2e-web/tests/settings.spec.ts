import { test, expect, type Page } from "@playwright/test";
import * as fs from "node:fs";

// Settings journey over the embedded `cortex serve` surface: the route renders
// its admin configuration content for the single-user local principal, the
// AppShell theme toggle flips the root theme token class, and no credential the
// operator supplied is ever rendered as text.
const serveLog = process.env.CORTEX_E2E_SERVE_LOG ?? "";
const baseURL = process.env.CORTEX_E2E_BASE_URL ?? "";
const apiToken = (process.env.CORTEX_HTTP_TOKEN ?? "").trim();
const uiToken = apiToken || "local-e2e-token";
const MINTED_KEY_PATTERN = /^(ctx_[A-Za-z0-9_-]+)$/m;

function readMintedWebKey(): string {
  const output = fs.readFileSync(serveLog, "utf8");
  const match = output.match(MINTED_KEY_PATTERN);
  if (!match) {
    throw new Error(`cortex e2e: no web access key printed in serve stdout:\n${output}`);
  }
  return match[1];
}

function authorizedHeaders(): Record<string, string> {
  return apiToken ? { Authorization: `Bearer ${apiToken}` } : {};
}

function primeRuntimeConfig(page: Page, serverUrl: string): Promise<void> {
  return page.addInitScript((url: string) => {
    (
      window as unknown as { __CORTEX_WEB_CONFIG__?: { serverUrl: string } }
    ).__CORTEX_WEB_CONFIG__ = { serverUrl: url };
  }, serverUrl);
}

async function connectControlRoom(page: Page): Promise<void> {
  await expect(page.getByRole("heading", { name: "Cortex Control Room" })).toBeVisible();
  await page.getByPlaceholder("cortex_sec_...").fill(uiToken);
  await page.getByRole("button", { name: "Conectar con Token" }).click();
}

test.describe("settings", () => {
  test("loads settings, toggles the theme token class, and never renders the secrets", async ({
    page,
    request,
  }) => {
    // REQ-SH-012: the parity /api/me route answers with the synthetic local
    // owner principal, so the admin-only settings surface must render instead
    // of the pre-parity restricted fallback.
    const me = await request.get(`${baseURL}/api/me`, { headers: authorizedHeaders() });
    expect(me.status(), await me.text()).toBe(200);
    const principal = (await me.json()) as { id: string; roles: string[] } | null;
    expect(principal).not.toBeNull();
    expect(principal?.roles.map((role) => role.toLowerCase())).toContain("owner");

    const webKey = readMintedWebKey();
    await primeRuntimeConfig(page, baseURL);

    await page.goto("/");
    await page.getByLabel("WEB ACCESS KEY").fill(webKey);
    await page.getByRole("button", { name: "Unlock Cortex" }).click();
    await connectControlRoom(page);

    // The minted web key persists across reloads, so the gate stays clear and
    // only the in-memory bearer session must be re-established on a direct load.
    const settingsLink = page.getByRole("link", { name: "Servidor" });
    if (await settingsLink.isVisible().catch(() => false)) {
      await settingsLink.click();
    } else {
      await page.goto("/settings");
      await connectControlRoom(page);
    }
    await expect(page).toHaveURL(/\/settings$/);

    await expect(
      page.getByRole("heading", { name: /Configuración de Servidor/ }),
    ).toBeVisible();
    await expect(page.getByText("Acceso Restringido")).toHaveCount(0);

    const rootHasLightClass = () =>
      page.evaluate(() => document.documentElement.classList.contains("light"));
    const storedTheme = () =>
      page.evaluate(() => window.localStorage.getItem("cortex_theme"));

    expect(await rootHasLightClass()).toBe(false);
    await page.getByRole("button", { name: "Claro", exact: true }).click();
    await expect.poll(rootHasLightClass).toBe(true);
    expect(await storedTheme()).toBe("light");

    await page.getByRole("button", { name: "Oscuro", exact: true }).click();
    await expect.poll(rootHasLightClass).toBe(false);
    expect(await storedTheme()).toBe("dark");

    const rendered = await page.locator("body").innerText();
    expect(rendered).not.toContain(uiToken);
    expect(rendered).not.toContain(webKey);
  });
});
