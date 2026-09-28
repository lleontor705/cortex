import { defineConfig, devices, chromium } from "@playwright/test";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";

// The embedded UI is compiled into the Go binary from internal/web/dist, so the
// harness boots the real binary rather than a Next.js dev server. State lives in
// a throwaway HOME/data directory so every run starts from a clean first boot.
function findRepoRoot(start: string): string {
  let dir = start;
  for (let depth = 0; depth < 8; depth += 1) {
    if (fs.existsSync(path.join(dir, "go.mod"))) return dir;
    const parent = path.dirname(dir);
    if (parent === dir) break;
    dir = parent;
  }
  throw new Error("cortex e2e: repository root not found (no go.mod above the config)");
}

const repoRoot = findRepoRoot(__dirname);

// Playwright evaluates this module in the orchestrating runner AND in every
// worker process. A pid-derived run identity would diverge between them, so the
// runner mints it once and shares it through the environment (workers inherit
// the runner's env); every process then resolves the same temp dir and port.
const inheritedRunId = process.env.CORTEX_E2E_RUN_ID;
const isRunnerProcess = inheritedRunId === undefined;
const runId = inheritedRunId ?? `${process.pid}-${Date.now().toString(36)}`;
process.env.CORTEX_E2E_RUN_ID = runId;

function seed(text: string): number {
  let hash = 0;
  for (let i = 0; i < text.length; i += 1) {
    hash = (hash * 31 + text.charCodeAt(i)) >>> 0;
  }
  return hash;
}

const host = "127.0.0.1";
// A run-derived port keeps concurrent local runs from colliding; CI can pin one.
const port =
  Number.parseInt(process.env.CORTEX_E2E_PORT ?? "", 10) || 34000 + (seed(runId) % 20000);
process.env.CORTEX_E2E_PORT = String(port);
const baseURL = `http://${host}:${port}`;

const tempRoot = path.join(os.tmpdir(), `cortex-e2e-web-${runId}`);
const homeDir = path.join(tempRoot, "home");
const dataDir = path.join(tempRoot, "data");
const serveLog = path.join(tempRoot, "serve.stdout.log");

if (isRunnerProcess) {
  // The run id is unique, so only the runner (re)creates this tree. Workers
  // load this module after the server is already writing its stdout there and
  // must never wipe it, or the minted key hand-off breaks.
  fs.rmSync(tempRoot, { recursive: true, force: true });
}
fs.mkdirSync(homeDir, { recursive: true });
fs.mkdirSync(dataDir, { recursive: true });

// Fail fast (never silently skip) when the browser is absent, and name the fix.
const chromiumPath = chromium.executablePath();
if (!fs.existsSync(chromiumPath)) {
  throw new Error(
    `cortex e2e: chromium is not installed (expected executable at ${chromiumPath}).\n` +
      "Install it once with: npx playwright install chromium",
  );
}

const serverEnv: Record<string, string> = {
  ...(process.env as Record<string, string>),
  HOME: homeDir,
  USERPROFILE: homeDir,
  CORTEX_DATABASE_IN_MEMORY: "false",
  CORTEX_DATABASE_PATH: path.join(dataDir, "cortex.db"),
  CORTEX_HTTP_HOST: host,
  CORTEX_HTTP_PORT: String(port),
  CORTEX_EMBEDDING_PROVIDER: "none",
  CORTEX_SEARCH_OLLAMA_AUTO_START: "false",
  CORTEX_LLM_API_KEY: "",
  CORTEX_EMBEDDING_API_KEY: "",
  OPENAI_API_KEY: "",
  ANTHROPIC_API_KEY: "",
  OLLAMA_BASE_URL: "",
  OLLAMA_HOST: "",
};

// Hand the runtime coordinates to the specs through the test process environment.
process.env.CORTEX_E2E_BASE_URL = baseURL;
process.env.CORTEX_E2E_SERVE_LOG = serveLog;
process.env.CORTEX_E2E_TEMP_DIR = tempRoot;

export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 90_000,
  expect: { timeout: 20_000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    headless: true,
    actionTimeout: 20_000,
    navigationTimeout: 45_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], headless: true, baseURL },
    },
  ],
  webServer: {
    // `cortex serve` has no data/port flags; it resolves both from the CORTEX_*
    // environment above. stdout is teed to a log so the once-printed key can be
    // read from the serve output (never from the at-rest web.key file).
    command: `go run ./cmd/cortex serve > "${serveLog}" 2>&1`,
    cwd: repoRoot,
    url: `${baseURL}/health`,
    timeout: 180_000,
    reuseExistingServer: false,
    env: serverEnv,
  },
});
