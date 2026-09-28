import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import * as fs from "node:fs";

// Boots the real embedded `cortex serve` surface and reaches `/graph`. The
// mount shell and every control are asserted by role/title; no pixel or canvas
// snapshot is used. The Sigma.js WebGL renderer and its layers are created only
// once a subgraph payload arrives, so this spec seeds a project that owns edges
// and pins both the renderer's host element and the WebGL layers Sigma attaches.
const serveLog = process.env.CORTEX_E2E_SERVE_LOG ?? "";
const baseURL = process.env.CORTEX_E2E_BASE_URL ?? "";
const apiToken = (process.env.CORTEX_HTTP_TOKEN ?? "").trim();
const uiToken = apiToken || "local-e2e-token";
const MINTED_KEY_PATTERN = /^(ctx_[A-Za-z0-9_-]+)$/m;

// Observations carry a NOT NULL foreign key to sessions, and the bundle-backed
// /api/graph/project-graph emits edges only for a project that owns both
// observations and graphstore edges. The seed builds that minimal graph in the
// default project so the page's first mount already has nodes to render.
const SEED_PROJECT = "default";
const SEED_TITLES = ["E2E Graph Alpha", "E2E Graph Beta", "E2E Graph Gamma"];
// A project nobody seeds: it must still resolve to the documented empty envelope.
const EMPTY_PROJECT = "e2e-web-graph-empty";

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

async function seedGraph(request: APIRequestContext): Promise<void> {
  const sessionID = `e2e-graph-${Date.now().toString(36)}`;
  const session = await request.post(`${baseURL}/api/sessions`, {
    headers: authorizedHeaders(),
    data: { id: sessionID, project: SEED_PROJECT, directory: "D:/tmp/cortex-e2e-web" },
  });
  expect(session.status(), await session.text()).toBe(201);

  const observationIDs: number[] = [];
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
    const created = (await observation.json()) as { id: number };
    observationIDs.push(created.id);
  }

  for (let index = 1; index < observationIDs.length; index += 1) {
    const edge = await request.post(`${baseURL}/api/graph/edges`, {
      headers: authorizedHeaders(),
      data: {
        from_obs_id: observationIDs[index - 1],
        to_obs_id: observationIDs[index],
        relation_type: "references",
        weight: 1,
        confidence: 1,
      },
    });
    expect(edge.status(), await edge.text()).toBe(201);
  }
}

test.describe("graph smoke", () => {
  test("mounts the Sigma shell over a bounded project subgraph", async ({ page, request }) => {
    await seedGraph(request);

    // Contract check on the parity route the page calls: a project with edges
    // yields a bounded subgraph that honours the max_nodes envelope.
    const subgraphResponse = await request.get(
      `${baseURL}/api/graph/project-graph?project=${SEED_PROJECT}&max_nodes=200`,
      { headers: authorizedHeaders() },
    );
    expect(subgraphResponse.status(), await subgraphResponse.text()).toBe(200);
    const subgraph = (await subgraphResponse.json()) as {
      root: string;
      nodes: unknown[];
      edges: unknown[];
      truncated: boolean;
    };
    expect(subgraph.root).toBe(SEED_PROJECT);
    expect(subgraph.nodes.length).toBeGreaterThanOrEqual(SEED_TITLES.length);
    expect(subgraph.edges.length).toBeGreaterThan(0);
    expect(subgraph.truncated).toBe(false);

    const boundedResponse = await request.get(
      `${baseURL}/api/graph/project-graph?project=${SEED_PROJECT}&max_nodes=1`,
      { headers: authorizedHeaders() },
    );
    expect(boundedResponse.status(), await boundedResponse.text()).toBe(200);
    const bounded = (await boundedResponse.json()) as { nodes: unknown[]; truncated: boolean };
    expect(bounded.nodes.length).toBeLessThanOrEqual(1);
    expect(bounded.truncated).toBe(true);

    // Documented empty state: an unseeded project resolves to the root-labelled
    // empty envelope on the same 200 path instead of a null body or an error, so
    // the page degrades to an empty graph rather than crashing.
    const emptyResponse = await request.get(
      `${baseURL}/api/graph/project-graph?project=${EMPTY_PROJECT}`,
      { headers: authorizedHeaders() },
    );
    expect(emptyResponse.status(), await emptyResponse.text()).toBe(200);
    const empty = (await emptyResponse.json()) as {
      root: string;
      nodes: unknown[] | null;
      edges: unknown[] | null;
      truncated: boolean;
    };
    expect(empty.root).toBe(EMPTY_PROJECT);
    expect(empty.nodes).toBeNull();
    expect(empty.edges).toBeNull();
    expect(empty.truncated).toBe(false);

    await openConnectedApp(page);

    // The SPA keeps the live bearer in memory only, so navigation must stay
    // client-side: a full reload would drop the authenticated client the graph
    // page needs to request its subgraph.
    await page.getByRole("link", { name: "Grafo" }).click();

    await expect(page.getByRole("heading", { name: "Grafo Cortex" })).toBeVisible();
    await expect(page.getByText("Sigma.js WebGL")).toBeVisible();

    await expect(page.getByRole("button", { name: /Grafo de Conocimiento/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Código y Arquitectura/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Grafo Unificado/ })).toBeVisible();

    await expect(page.getByPlaceholder("Buscar nodos o conceptos...")).toBeVisible();
    await expect(page.getByText("FILTRAR TIPOS:")).toBeVisible();

    // The WebGL mount shell: the single element the Sigma.js renderer attaches to.
    const mount = page.locator('div[class*="cursor-grab"]');
    await expect(mount).toHaveCount(1);
    await expect(mount).toBeVisible();

    // Sigma.js attaches its layers as canvas children of the mount shell only
    // after a subgraph payload arrives. A live WebGL context backing a sized
    // nodes layer is the deterministic "nodes rendered" signal for a canvas.
    const nodesLayer = mount.locator("canvas.sigma-nodes");
    await expect(nodesLayer).toBeVisible();
    await expect(mount.locator("canvas.sigma-edges")).toHaveCount(1);
    await expect(mount.locator("canvas.sigma-mouse")).toHaveCount(1);

    const webglMounted = await nodesLayer.evaluate((element) => {
      const canvas = element as HTMLCanvasElement;
      const context = canvas.getContext("webgl2") || canvas.getContext("webgl");
      return context !== null && canvas.width > 0 && canvas.height > 0;
    });
    expect(webglMounted).toBe(true);

    await expect(page.getByTitle("Acercar (Zoom In)")).toBeVisible();
    await expect(page.getByTitle("Alejar (Zoom Out)")).toBeVisible();
    await expect(page.getByTitle("Restablecer Vista Centrada")).toBeVisible();
  });
});
