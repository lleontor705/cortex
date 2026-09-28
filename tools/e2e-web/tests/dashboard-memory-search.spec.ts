import { test, expect, type Page } from "@playwright/test";
import * as fs from "node:fs";

// The embedded UI is compiled into the Go binary, so every spec boots the real
// `cortex serve` surface. Three harness facts drive this file:
//   * the minted web key is read from the tee'd serve stdout (never web.key);
//   * `cortex serve` binds the REST bearer to CORTEX_HTTP_TOKEN, which the
//     harness inherits from the ambient environment, so the same value must be
//     presented to /api/* (an unset variable leaves /api/* unauthenticated,
//     where any non-empty value satisfies the UI connect probe);
//   * the exported shell is not shipped a /config.js tag, so the UI would fall
//     back to its documented localhost:7438 origin: the runtime endpoint the Go
//     handler would emit is published explicitly before the bundle loads.
const serveLog = process.env.CORTEX_E2E_SERVE_LOG ?? "";
const baseURL = process.env.CORTEX_E2E_BASE_URL ?? "";
const apiToken = (process.env.CORTEX_HTTP_TOKEN ?? "").trim();
const uiToken = apiToken || "local-e2e-token";
const MINTED_KEY_PATTERN = /^(ctx_[A-Za-z0-9_-]+)$/m;

const SEED_PROJECT = "e2e-web-dashboard";
const SEED_TITLES = ["E2E Dashboard Alpha", "E2E Dashboard Beta", "E2E Search Gamma"];

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

async function openConnectedApp(page: Page): Promise<void> {
  await page.addInitScript((url: string) => {
    (
      window as unknown as { __CORTEX_WEB_CONFIG__?: { serverUrl: string } }
    ).__CORTEX_WEB_CONFIG__ = { serverUrl: url };
  }, baseURL);

  await page.goto("/");
  await page.getByLabel("WEB ACCESS KEY").fill(readMintedWebKey());
  await page.getByRole("button", { name: "Unlock Cortex" }).click();
  await expect(page.getByRole("heading", { name: "Cortex Control Room" })).toBeVisible();
  await page.getByPlaceholder("cortex_sec_...").fill(uiToken);
  await page.getByRole("button", { name: "Conectar con Token" }).click();
  await expect(page.getByRole("heading", { name: /Bienvenido|Control Room de Memoria/ })).toBeVisible(
    { timeout: 30_000 },
  );
}

// Observations carry a NOT NULL foreign key to sessions, so the seed creates its
// session first and then posts each observation through the REST contract the
// dashboard and search surfaces read back.
async function seedMemory(
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const sessionID = `e2e-dash-${Date.now().toString(36)}`;
  const session = await request.post(`${baseURL}/api/sessions`, {
    headers: authorizedHeaders(),
    data: { id: sessionID, project: SEED_PROJECT, directory: "D:/tmp/cortex-e2e-web" },
  });
  expect(session.status(), await session.text()).toBe(201);

  for (const title of SEED_TITLES) {
    const observation = await request.post(`${baseURL}/api/observations`, {
      headers: authorizedHeaders(),
      data: {
        session_id: sessionID,
        title,
        content: `seeded harness content for ${title}`,
        type: "discovery",
        project: SEED_PROJECT,
        scope: "project",
      },
    });
    expect(observation.status(), await observation.text()).toBe(201);
  }
  return sessionID;
}

test.describe("dashboard memory listing and memory search", () => {
  test("seeded observations render in the dashboard listing", async ({ page, request }) => {
    const sessionID = await seedMemory(request);

    await openConnectedApp(page);

    for (const title of SEED_TITLES) {
      await expect(page.getByText(title, { exact: true })).toBeVisible();
    }
    await expect(page.getByText(`ID: ${sessionID.slice(0, 12)}...`).first()).toBeVisible();
  });

  test("seeded memory is searchable and the search view mounts", async ({ page, request }) => {
    await seedMemory(request);

    const search = await request.get(`${baseURL}/api/search/hybrid?q=Gamma&limit=10`, {
      headers: authorizedHeaders(),
    });
    expect(search.status(), await search.text()).toBe(200);
    const results = (await search.json()) as Array<{ title: string; project: string }>;
    expect(results.map((hit) => hit.title)).toContain("E2E Search Gamma");
    expect(results.filter((hit) => hit.project === SEED_PROJECT).length).toBeGreaterThan(0);

    await openConnectedApp(page);
    await page.getByRole("link", { name: "Explorar" }).click();

    await expect(page.getByRole("heading", { name: "Encuentra memoria y código" })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Todo/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Memoria/ })).toBeVisible();

    // The scope picker stays disabled until /api/me yields a principal and
    // /api/agent/projects resolves it; the parity routes make both real, so the
    // selector must now be interactive and the query must run end-to-end.
    const projectSelector = page.getByLabel("Proyecto autorizado");
    await expect(projectSelector).toBeEnabled();
    await projectSelector.selectOption(SEED_PROJECT);

    const queryInput = page.getByLabel("Consulta de conocimiento");
    await expect(queryInput).toBeEnabled();
    await queryInput.fill("Gamma");
    await page.getByRole("button", { name: "Buscar" }).click();

    await expect(page.getByRole("heading", { name: "E2E Search Gamma" }).first()).toBeVisible();
  });
});
