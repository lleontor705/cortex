/**
 * Cortex — OpenCode plugin adapter
 *
 * Thin layer that connects OpenCode's event system to the Cortex Go binary.
 * The Go binary runs as a local HTTP server and handles all persistence.
 *
 * Flow:
 *   OpenCode events → this plugin → HTTP calls → cortex serve → SQLite
 *
 * Session resilience:
 *   Uses `ensureSession()` before any DB write. Sessions are created on-demand
 *   even if the plugin was loaded after the session started.
 *
 * Delivery contract (REM-PLUGIN-001):
 *   - Credentials come only from CORTEX_HTTP_TOKEN; without it nothing is sent.
 *   - Success is only a 2xx response with the exact persisted body expected
 *     for the endpoint; every other outcome is classified (unauthorized,
 *     forbidden, conflict, validation, unavailable, timeout,
 *     invalid_response, config, durable_rejected) and logged bounded.
 *   - Sessions are confirmed before use: ensureSession caches a session only
 *     on a persisted-identity echo or a 409 proven by reading the exact
 *     persisted identity back, and classifies every failure instead of
 *     silently assuming continuity. Protected writes and trusted context
 *     require that confirmation (SEC-04).
 *   - Logs never contain tokens, payloads, or response bodies.
 *   - Hooks always return control; every delivery is deadline-bounded.
 *   - Prompts and passive captures are truncated by UTF-8 runes before JSON
 *     with truncated/original_bytes/stored_bytes metadata.
 *   - Durable handoffs (cortex_handoff) are an MCP capability already
 *     delivered through the MCP channel; this plugin never interprets,
 *     redelivers, or fabricates signals from their results.
 */

import type { Plugin } from "@opencode-ai/plugin"

// ─── Configuration ───────────────────────────────────────────────────────────

const CORTEX_HTTP_PORT = parseInt(process.env.CORTEX_HTTP_PORT ?? "7438")
const CORTEX_URL = (process.env.CORTEX_SERVER_URL ?? process.env.CORTEX_URL ?? `http://127.0.0.1:${CORTEX_HTTP_PORT}`).replace(/\/+$/, "")
const CORTEX_HTTP_TOKEN = (process.env.CORTEX_HTTP_TOKEN ?? "").trim()
const CORTEX_BIN = process.env.CORTEX_BIN ?? (() => {
  // Try Bun.which for PATH lookup, fall back to bare command
  try {
    const which = (globalThis as any).Bun?.which?.("cortex")
    if (which) return which
  } catch {}
  return "cortex"
})()

const REQUEST_TIMEOUT_MS = 2000
const PAYLOAD_BYTE_LIMIT = 2000
const encoder = new TextEncoder()

export type CortexMode = "server" | "local"

let cachedMode: CortexMode | null = null

export async function detectCortexMode(): Promise<CortexMode> {
  if (cachedMode) return cachedMode
  if (!CORTEX_HTTP_TOKEN) {
    cachedMode = "local"
    return "local"
  }
  try {
    const res = await boundedFetch("/api/me", {
      headers: { Authorization: `Bearer ${CORTEX_HTTP_TOKEN}` },
    })
    if (res.status === 200 || res.status === 403) {
      cachedMode = "server"
      return "server"
    }
  } catch {}
  cachedMode = "local"
  return "local"
}

// Cortex's own MCP tools — don't count these as "tool calls" for session stats.
// cortex_handoff is handled separately before this set is consulted.
const CORTEX_TOOLS = new Set([
  // Core memory
  "cortex_search",
  "cortex_save",
  "cortex_update",
  "cortex_delete",
  "cortex_suggest_topic_key",
  "cortex_save_prompt",
  "cortex_session_summary",
  "cortex_context",
  "cortex_stats",
  "cortex_timeline",
  "cortex_get_observation",
  "cortex_session_start",
  "cortex_session_end",
  "cortex_capture_passive",
  // Knowledge graph & Architecture
  "cortex_relate",
  "cortex_graph",
  "cortex_graph_relationships",
  "cortex_graph_path",
  "cortex_graph_subgraph",
  "cortex_score",
  "cortex_archive",
  "cortex_search_hybrid",
  "cortex_get_blast_radius",
  "cortex_analyze_architecture",
  "cortex_detect_cycles",
  "cortex_ingest_code",
  // Governance, Skills & System Context
  "cortex_get_project_context",
  "cortex_list_skills",
  "cortex_get_skill",
  "cortex_resolve_query",
  "cortex_get_status",
  // History & Hygiene
  "cortex_revision_history",
  "cortex_consolidate",
  "cortex_project_dna",
  "cortex_merge_projects",
  "cortex_handoff",
  // Temporal (advanced)
  "cortex_temporal_create_edge",
  "cortex_temporal_get_edges",
  "cortex_temporal_get_relevant",
  "cortex_temporal_create_snapshot",
  "cortex_temporal_record_operation",
  "cortex_temporal_evaluate_quality",
  "cortex_temporal_system_metrics",
  "cortex_temporal_health_check",
  "cortex_temporal_evolution_path",
  "cortex_temporal_fact_state",
  "cortex_search_temporal",
])

// ─── Mode-Aware Memory Instructions ──────────────────────────────────────────

export function buildMemoryInstructions(mode: CortexMode = "server"): string {
  if (mode === "server") {
    return `## Cortex Persistent Memory — Protocol (Mode: SERVER / PostgreSQL Multi-Tenant)

You have access to Cortex Server via authenticated MCP tools (PostgreSQL RLS, PGVector semantic search, knowledge graph, corporate governance, and dynamic skills).

TRANSPORT IDENTIFIERS:
- In Server Mode, all observation IDs and node IDs are public UUID strings (e.g. "6b806a41-9e9b-4298-a39a-7887a71e94e0").
- Never use or fabricate numeric IDs.

### 1. STARTUP: PROJECT CONTEXT & GOVERNANCE (Mandatory at session start)
At the beginning of any session or when switching projects:
1. IMMEDIATELY call \`cortex_get_project_context(project)\` to retrieve corporate governance rules, architectural constraints, and available skill playbooks.
2. Call \`cortex_list_skills(project)\` and \`cortex_get_skill(key, project)\` to inspect and follow approved domain skills.

### 2. WHEN TO SAVE (Mandatory after completing work)
Call \`cortex_save\` IMMEDIATELY after any of these:
- Bug fix completed (type: "bugfix")
- Architectural/design decision made (type: "decision")
- Non-obvious discovery in codebase (type: "discovery")
- Pattern or convention established (type: "pattern")
- Configuration/environment rule (type: "config")
- Domain learning (type: "learning")

Format for \`cortex_save\`:
- **title**: Verb + object — short, searchable (e.g. "Fixed N+1 query in PgBouncer pool")
- **type**: bugfix | decision | pattern | discovery | config | learning
- **scope**: \`project\` (default) | \`personal\`
- **topic_key** (optional): stable key for evolving topics (e.g. \`auth/jwt-rotation\`)
- **content**: What was done, Why it was done, Affected files, and Lessons learned.

### 3. CODEBASE INTELLIGENCE & BLAST RADIUS
- Before refactoring or renaming symbols, call \`cortex_get_blast_radius(node_id)\` to calculate all impacted downstream callers, files, and dependencies.
- Call \`cortex_detect_cycles(project)\` to ensure no circular import dependencies or architectural violations.
- Call \`cortex_analyze_architecture(project)\` to inspect code communities and god nodes.
- Use \`cortex_relate\` to link related observations (references, relates_to, follows, supersedes, contradicts).

### 4. UNIFIED SEARCH & QUERY RESOLUTION
- For complex questions or domain lookups, call \`cortex_resolve_query(query, project)\` for a unified retrieval across corporate rules, skills, and observations.
- Call \`cortex_search\` for keyword/FTS search, or \`cortex_context\` for recent session history.
- If you find an observation match, call \`cortex_get_observation(id)\` to read its complete full text.

### 5. SESSION CLOSE PROTOCOL (Mandatory before ending)
Before saying "done" or finishing a session:
1. Call \`cortex_session_summary\` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
This is NOT optional. Without this, the next session or agent starts blind.

### 6. AFTER COMPACTION
1. IMMEDIATELY call \`cortex_session_summary\` with the compacted summary content.
2. Then call \`cortex_context\` to recover context from previous sessions before continuing.`
  }

  return `## Cortex Persistent Memory — Protocol (Mode: LOCAL / SQLite Zero-CGO)

You have access to Cortex Local, a high-performance local memory system (zero-CGO SQLite, FTS5 full-text search, knowledge graph, and temporal tracking).

TRANSPORT IDENTIFIERS:
- In Local Mode, observation and graph IDs are numeric integers (e.g. 1, 42).
- Follow active MCP tool schema.

### 1. WHEN TO SAVE (Mandatory after completing work)
Call \`cortex_save\` IMMEDIATELY after any of these:
- Bug fix completed (type: "bugfix")
- Architecture decision made (type: "decision")
- Non-obvious discovery about codebase (type: "discovery")
- Pattern established (type: "pattern")
- Configuration/environment rule (type: "config")
- Learning (type: "learning")

Format for \`cortex_save\`:
- **title**: Verb + object — short, searchable (e.g. "Fixed N+1 query in UserList")
- **type**: bugfix | decision | pattern | discovery | config | learning
- **scope**: \`project\` (default) | \`personal\`
- **topic_key** (optional, recommended): stable key like \`architecture/auth-model\`
- **content**: What was done, Why, Where (files affected), and Gotchas.

### 2. KNOWLEDGE GRAPH & RELATIONS
- After saving related observations, call \`cortex_relate\` (references, relates_to, follows, supersedes, contradicts).
- Call \`cortex_graph\` to traverse connections from any observation.
- Call \`cortex_score\` to recalculate observation importance.

### 3. SEARCH & RETRIEVAL
1. First call \`cortex_context\` to check recent session history.
2. If not found, call \`cortex_search\` with keywords (FTS5).
3. If needed, call \`cortex_search_hybrid\` for combined vector + FTS search.
4. Call \`cortex_get_observation(id)\` to fetch the complete full-text observation.

### 4. REVISION HISTORY & HYGIENE
- \`cortex_revision_history(id)\`: View evolution across upserts.
- \`cortex_timeline(id)\`: Chronological context.
- \`cortex_archive(id)\`: Archive obsolete observations.
- \`cortex_delete(id, hard_delete: true)\`: Permanently delete.

### 5. SESSION CLOSE PROTOCOL (Mandatory before ending)
Before saying "done" or finishing a session:
1. Call \`cortex_session_summary\` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.

### 6. AFTER COMPACTION
1. Call \`cortex_session_summary\` with the compacted summary content.
2. Call \`cortex_context\` to recover context before resuming work.`
}

const MEMORY_INSTRUCTIONS = buildMemoryInstructions("server")

// ─── Delivery classification ─────────────────────────────────────────────────

type DeliveryKind = "prompt" | "observation" | "session"

type Classification =
  | "success"
  | "unauthorized"
  | "forbidden"
  | "conflict"
  | "validation"
  | "unavailable"
  | "timeout"
  | "invalid_response"
  | "config"
  | "durable_rejected"

// Logs carry the classification only — never tokens, payloads, or bodies.
function report(kind: DeliveryKind, outcome: Classification, detail = ""): void {
  const suffix = detail ? ` (${detail})` : ""
  if (outcome === "success") {
    console.info(`[cortex] ${kind} delivery success`)
  } else {
    console.warn(`[cortex] ${kind} delivery ${outcome}${suffix}`)
  }
}

function classifyStatus(status: number): Classification {
  if (status === 401) return "unauthorized"
  if (status === 403) return "forbidden"
  if (status === 409) return "conflict"
  if (status === 400 || status === 404 || status === 405 || status === 413 || status === 422) {
    return "validation"
  }
  return "unavailable"
}

function classifyException(err: unknown): Classification {
  const name = (err as DOMException | null)?.name
  if (name === "TimeoutError" || name === "AbortError") return "timeout"
  return "unavailable"
}

// ─── HTTP Client ─────────────────────────────────────────────────────────────

// fetch with a deadline the host runtime can observe. The timer (not
// AbortSignal.timeout) drives aborts so deadlines stay testable and the
// request can never hang the hook.
async function boundedFetch(path: string, init: RequestInit = {}): Promise<Response> {
  const controller = new AbortController()
  let deadlineHit = false
  const timer = setTimeout(() => {
    deadlineHit = true
    controller.abort()
  }, REQUEST_TIMEOUT_MS)
  try {
    return await fetch(`${CORTEX_URL}${path}`, { ...init, signal: controller.signal })
  } catch (err) {
    if (deadlineHit) {
      throw Object.assign(new Error("deadline elapsed"), { name: "TimeoutError" })
    }
    throw err
  } finally {
    clearTimeout(timer)
  }
}

type HttpResult =
  | { ok: true; status: number; body: unknown }
  | { ok: false; classification: Classification }

async function request(path: string, body?: unknown): Promise<HttpResult> {
  try {
    // Every request carries the credential when one is configured, including
    // protected GETs. /health is the only unauthenticated route and is probed
    // through boundedFetch directly, never here.
    const headers: Record<string, string> = {}
    if (CORTEX_HTTP_TOKEN) headers.Authorization = `Bearer ${CORTEX_HTTP_TOKEN}`
    const init: RequestInit =
      body === undefined
        ? { headers }
        : {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              ...headers,
            },
            body: JSON.stringify(body),
          }
    const res = await boundedFetch(path, init)
    if (res.status < 200 || res.status >= 300) {
      return { ok: false, classification: classifyStatus(res.status) }
    }
    try {
      return { ok: true, status: res.status, body: await res.json() }
    } catch {
      return { ok: false, classification: "invalid_response" }
    }
  } catch (err) {
    return { ok: false, classification: classifyException(err) }
  }
}

// Deliver a payload and classify the outcome. `expected` must validate the
// exact persisted body for the endpoint; anything else is invalid_response.
async function deliver(
  kind: DeliveryKind,
  path: string,
  payload: unknown,
  expected: (body: unknown) => boolean
): Promise<void> {
  if (!CORTEX_HTTP_TOKEN) {
    report(kind, "config", "missing CORTEX_HTTP_TOKEN")
    return
  }
  const result = await request(path, payload)
  if (!result.ok) {
    report(kind, result.classification)
    return
  }
  if (!expected(result.body)) {
    report(kind, "invalid_response")
    return
  }
  report(kind, "success")
}

async function isCortexRunning(): Promise<boolean> {
  try {
    const res = await boundedFetch("/health")
    return res.ok
  } catch {
    return false
  }
}

// ─── UTF-8 byte-bounded truncation ───────────────────────────────────────────

export type TruncationRecord = {
  content: string
  truncated: boolean
  original_bytes: number
  stored_bytes: number
}

// Truncate by whole UTF-8 runes so stored bytes never exceed the limit and
// the stored content always round-trips as valid UTF-8. Array.from indexes by
// codepoint, so an astral rune (surrogate pair) can never be split into a
// lone surrogate that would encode as U+FFFD.
function truncateUtf8(text: string): TruncationRecord {
  const originalBytes = encoder.encode(text).byteLength
  if (originalBytes <= PAYLOAD_BYTE_LIMIT) {
    return { content: text, truncated: false, original_bytes: originalBytes, stored_bytes: originalBytes }
  }
  const runes = Array.from(text)
  let lo = 0
  let hi = runes.length
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2)
    const prefix = runes.slice(0, mid).join("")
    if (encoder.encode(prefix).byteLength <= PAYLOAD_BYTE_LIMIT) {
      lo = mid
    } else {
      hi = mid - 1
    }
  }
  const content = runes.slice(0, lo).join("")
  const storedBytes = encoder.encode(content).byteLength
  return { content, truncated: true, original_bytes: originalBytes, stored_bytes: storedBytes }
}

// ─── Response body contracts ─────────────────────────────────────────────────

function hasExactKeys(body: unknown, keys: readonly string[]): body is Record<string, unknown> {
  if (typeof body !== "object" || body === null || Array.isArray(body)) return false
  const actual = Object.keys(body)
  return actual.length === keys.length && keys.every((key) => key in body)
}

const PROMPT_KEYS = ["id", "content", "project", "session_id", "created_at"] as const

function isPositiveID(value: unknown): boolean {
  return typeof value === "number" && Number.isInteger(value) && value > 0
}

function persistedPrompt(sent: { content: string; session_id: string }): (body: unknown) => boolean {
  return (body) =>
    hasExactKeys(body, PROMPT_KEYS) &&
    isPositiveID(body.id) &&
    typeof body.content === "string" &&
    body.content === sent.content &&
    typeof body.project === "string" &&
    body.session_id === sent.session_id &&
    typeof body.created_at === "string"
}

// The persisted Observation is validated by identity, not by exact shape:
// a positive ID plus equality on every field the plugin actually sent.
// Additive server fields (tags, sync metadata, ...) are tolerated so the
// contract does not break when the server payload grows.
function persistedObservation(
  sent: {
    content: string
    session_id: string
    title: string
    project: string
    type: string
    scope: string
  }
): (body: unknown) => boolean {
  return (body) =>
    isJsonObject(body) &&
    isPositiveID(body.id) &&
    body.session_id === sent.session_id &&
    body.content === sent.content &&
    body.title === sent.title &&
    body.project === sent.project &&
    body.type === sent.type &&
    body.scope === sent.scope
}

// POST /api/sessions/{id}/end returns exactly {"status":"ended"}; anything
// else in a 2xx body is a false success.
function persistedEndStatus(): (body: unknown) => boolean {
  return (body) => hasExactKeys(body, ["status"]) && body.status === "ended"
}

function isJsonObject(body: unknown): boolean {
  return typeof body === "object" && body !== null && !Array.isArray(body)
}

const SESSION_REQUIRED_KEYS = ["id", "project", "directory", "started_at"] as const

// Context injected into the host prompt is trusted only when every item is
// an object with string title and type; anything else is invalid_response.
function isObservationSummaryList(body: unknown): body is Array<{ title: string; type: string }> {
  return (
    Array.isArray(body) &&
    body.every(
      (item) => isJsonObject(item) &&
        typeof (item as Record<string, unknown>).title === "string" &&
        typeof (item as Record<string, unknown>).type === "string"
    )
  )
}

// The persisted Session echo is validated by identity: the server returns the
// session that was sent (optional omitempty fields are not asserted).
function persistedSession(sent: {  id: string
  project: string
  directory: string
}): (body: unknown) => boolean {
  return (body) => {
    if (!isJsonObject(body)) return false
    const record = body as Record<string, unknown>
    return (
      SESSION_REQUIRED_KEYS.every((key) => key in record) &&
      record.id === sent.id &&
      record.project === sent.project &&
      record.directory === sent.directory &&
      typeof record.started_at === "string"
    )
  }
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

function extractProjectName(directory: string): string {
  try {
    const result = Bun.spawnSync(["git", "-C", directory, "remote", "get-url", "origin"])
    if (result.exitCode === 0) {
      const url = result.stdout?.toString().trim()
      if (url) {
        const name = url.replace(/\.git$/, "").split(/[/:]/).pop()
        if (name) return name
      }
    }
  } catch {}

  try {
    const result = Bun.spawnSync(["git", "-C", directory, "rev-parse", "--show-toplevel"])
    if (result.exitCode === 0) {
      const root = result.stdout?.toString().trim()
      if (root) return root.split("/").pop() ?? "unknown"
    }
  } catch {}

  return directory.split("/").pop() ?? "unknown"
}

function stripPrivateTags(str: string): string {
  if (!str) return ""
  return str.replace(/<private>[\s\S]*?<\/private>/gi, "[REDACTED]").trim()
}

// ─── Durable handoff neutrality ──────────────────────────────────────────────

// cortex_handoff is an MCP capability: by the time this hook sees a result,
// the handoff was already delivered (or failed) through the MCP channel.
// The plugin never interprets, redelivers, truncates, or classifies that
// result — doing so would fabricate delivery signals from tool output.

// ─── Plugin Export ───────────────────────────────────────────────────────────

export const Cortex: Plugin = async (ctx) => {
  const project = extractProjectName(ctx.directory)

  const toolCounts = new Map<string, number>()
  const knownSessions = new Set<string>()
  const subAgentSessions = new Set<string>()

  type SessionEnsureResult = {
    confirmed: boolean
    classification: Classification
  }

  // A session counts as known only after the server confirmed it: a 2xx
  // persisted echo of the exact session identity, or a 409 conflict proven
  // by reading the exact persisted identity back. A bare 409 body is an
  // error object and cannot prove which session exists, so it is never
  // trusted on its own. Failures are classified, never cached, and a later
  // event retries instead of assuming continuity.
  async function ensureSession(sessionId: string): Promise<SessionEnsureResult> {
    if (!sessionId || subAgentSessions.has(sessionId)) {
      return { confirmed: false, classification: "validation" }
    }
    if (knownSessions.has(sessionId)) {
      return { confirmed: true, classification: "success" }
    }
    if (!CORTEX_HTTP_TOKEN) {
      report("session", "config", "missing CORTEX_HTTP_TOKEN")
      return { confirmed: false, classification: "config" }
    }
    const sent = { id: sessionId, project, directory: ctx.directory }
    const result = await request("/api/sessions", sent)
    if (result.ok) {
      if (persistedSession(sent)(result.body)) {
        knownSessions.add(sessionId)
        return { confirmed: true, classification: "success" }
      }
      report("session", "invalid_response")
      return { confirmed: false, classification: "invalid_response" }
    }
    if (result.classification === "conflict") {
      // Contract-proven conflict: confirm the exact existing identity through
      // the read endpoint before trusting the session.
      const existing = await request(`/api/sessions/${encodeURIComponent(sessionId)}`)
      if (existing.ok && persistedSession(sent)(existing.body)) {
        knownSessions.add(sessionId)
        return { confirmed: true, classification: "success" }
      }
      const classification = existing.ok ? "invalid_response" : existing.classification
      report("session", classification)
      return { confirmed: false, classification }
    }
    report("session", result.classification)
    return { confirmed: false, classification: result.classification }
  }

  // Try to start cortex server if not running. Never attempted without a
  // configured credential: an unauthenticated plugin sends nothing.
  if (CORTEX_HTTP_TOKEN) {
    const running = await isCortexRunning()
    if (!running) {
      try {
        Bun.spawn([CORTEX_BIN, "serve"], {
          stdout: "ignore",
          stderr: "ignore",
          stdin: "ignore",
        })
        await new Promise((r) => setTimeout(r, 500))
      } catch {}
    }
  }

  return {
    // ─── Event Listeners ───────────────────────────────────────────

    event: async ({ event }) => {
      if (event.type === "session.created") {
        const info = (event.properties as any)?.info
        const sessionId = info?.id
        const parentID = info?.parentID
        const title: string = info?.title ?? ""

        const isSubAgent = !!parentID || title.endsWith(" subagent)")

        if (sessionId && !isSubAgent) {
          await ensureSession(sessionId)
        } else if (sessionId && isSubAgent) {
          subAgentSessions.add(sessionId)
        }
      }

      if (event.type === "session.deleted") {
        const info = (event.properties as any)?.info
        const sessionId = info?.id
        if (sessionId) {
          if (!subAgentSessions.has(sessionId) && knownSessions.has(sessionId)) {
            await deliver(
              "session",
              `/api/sessions/${encodeURIComponent(sessionId)}/end`,
              { summary: "" },
              persistedEndStatus()
            )
          }
          toolCounts.delete(sessionId)
          knownSessions.delete(sessionId)
          subAgentSessions.delete(sessionId)
        }
      }
    },

    // ─── User Prompt Capture ──────────────────────────────────────

    "chat.message": async (input, output) => {
      try {
        if (subAgentSessions.has(input.sessionID)) return

        const sessionId = input.sessionID
        const content = output.parts
          .filter((p) => p.type === "text")
          .map((p) => (p as any).text ?? "")
          .join("\n")
          .trim()

        const fallback = !content && output.message.summary
          ? `${output.message.summary.title ?? ""}\n${output.message.summary.body ?? ""}`.trim()
          : ""

        const finalContent = content || fallback

        if (finalContent.length > 10) {
          // SEC-04: no protected write unless the exact session was
          // positively confirmed. Failures stay uncached and retryable on a
          // later host event.
          const session = await ensureSession(sessionId)
          if (!session.confirmed) return
          const redacted = stripPrivateTags(finalContent)
          const record = truncateUtf8(redacted)
          await deliver(
            "prompt",
            "/api/prompts",
            {
              session_id: sessionId,
              project,
              ...record,
            },
            persistedPrompt({ content: record.content, session_id: sessionId })
          )
        }
      } catch {
        report("prompt", "unavailable")
      }
    },

    // ─── Tool Execution Hook ─────────────────────────────────────

    "tool.execute.after": async (input, output) => {
      try {
        const tool = input.tool.toLowerCase()

        // Durable handoffs were already delivered through the MCP channel;
        // their result is not input to this plugin. Stay neutral: no
        // interpretation, no redelivery, no fabricated signal.
        if (tool === "cortex_handoff") return

        if (CORTEX_TOOLS.has(tool)) return

        const sessionId = input.sessionID
        let sessionConfirmed = false
        if (sessionId) {
          const session = await ensureSession(sessionId)
          sessionConfirmed = session.confirmed
          toolCounts.set(sessionId, (toolCounts.get(sessionId) ?? 0) + 1)
        }

        // Passive capture from Task tool output — protected write, so it
        // requires the same confirmed session.
        if (input.tool === "Task" && output && sessionId && sessionConfirmed) {
          const text = typeof output === "string" ? output : JSON.stringify(output)
          if (text.length > 50) {
            const redacted = stripPrivateTags(text)
            const record = truncateUtf8(redacted)
            const sent = {
              content: record.content,
              session_id: sessionId,
              title: "Passive capture from task",
              project,
              type: "passive",
              scope: "project",
            }
            await deliver(
              "observation",
              "/api/observations",
              {
                session_id: sent.session_id,
                title: sent.title,
                type: sent.type,
                project: sent.project,
                scope: sent.scope,
                ...record,
              },
              persistedObservation(sent)
            )
          }
        }
      } catch {
        report("observation", "unavailable")
      }
    },

    // ─── System Prompt: Always-on memory instructions ──────────

    "experimental.chat.system.transform": async (_input, output) => {
      const mode = await detectCortexMode()
      const instructions = buildMemoryInstructions(mode)
      if (output.system.length > 0) {
        output.system[output.system.length - 1] += "\n\n" + instructions
      } else {
        output.system.push(instructions)
      }
    },

    // ─── Compaction Hook ──────────────────────────────────────────

    "experimental.session.compacting": async (input, output) => {
      try {
        // SEC-04: returned context is trusted only when a credential is
        // configured and the exact session was positively confirmed.
        const session = input.sessionID
          ? await ensureSession(input.sessionID)
          : { confirmed: false, classification: "validation" as Classification }
        if (CORTEX_HTTP_TOKEN && session.confirmed) {
          const result = await request(
            `/api/observations?project=${encodeURIComponent(project)}&limit=20`
          )
          if (!result.ok) {
            report("observation", result.classification)
          } else if (!isObservationSummaryList(result.body)) {
            report("observation", "invalid_response")
          } else if (result.body.length > 0) {
            const ctx = (result.body as any[]).map((o) => `- [${o.type}] ${o.title}`).join("\n")
            output.context.push(`Recent Cortex memories for ${project}:\n${ctx}`)
          }
        }

        output.context.push(
          `CRITICAL INSTRUCTION FOR COMPACTED SUMMARY:\n` +
          `The agent has access to Cortex persistent memory via MCP tools.\n` +
          `You MUST include the following instruction at the TOP of the compacted summary:\n\n` +
          `"FIRST ACTION REQUIRED: Call cortex_session_summary with the content of this compacted summary. ` +
          `Use project: '${project}'. This preserves what was accomplished before compaction. Do this BEFORE any other work."\n\n` +
          `This is NOT optional. Without this, everything done before compaction is lost from memory.`
        )
      } catch {
        // Non-blocking: a hook failure must never break the host compaction.
      }
    },
  }
}
