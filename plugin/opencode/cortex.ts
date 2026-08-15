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
 *     on a persisted-identity echo or an explicit 409, and classifies every
 *     failure instead of silently assuming continuity.
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
const CORTEX_URL = `http://127.0.0.1:${CORTEX_HTTP_PORT}`
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
  // Knowledge graph
  "cortex_relate",
  "cortex_graph",
  "cortex_score",
  "cortex_archive",
  "cortex_search_hybrid",
  // Cortex additions
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
])

// ─── Memory Instructions ─────────────────────────────────────────────────────

const MEMORY_INSTRUCTIONS = `## Cortex Persistent Memory — Protocol

You have access to Cortex, a persistent memory system with knowledge graph, importance scoring,
full-text search, revision history, and temporal tracking that survives across sessions and compactions.

TRANSPORT IDS: Follow the active MCP tool schema. Local observation and graph IDs are numeric; Cortex Server IDs are public UUID strings. Never convert or reuse IDs across transports.

### WHEN TO SAVE (mandatory — not optional)

Call \`cortex_save\` IMMEDIATELY after any of these:
- Bug fix completed
- Architecture or design decision made
- Non-obvious discovery about the codebase
- Configuration change or environment setup
- Pattern established (naming, structure, convention)
- User preference or constraint learned

Format for \`cortex_save\`:
- **title**: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList")
- **type**: bugfix | decision | architecture | discovery | pattern | config | learning
- **scope**: \`project\` (default) | \`personal\`
- **topic_key** (optional, recommended): stable key like \`architecture/auth-model\`
- **content**:
  **What**: One sentence — what was done
  **Why**: What motivated it
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases (omit if none)

Topic rules:
- Different topics must not overwrite each other
- Reuse the same \`topic_key\` to update an evolving topic (upsert)
- If unsure about the key, call \`cortex_suggest_topic_key\` first
- Use \`cortex_update\` when you have an exact observation ID to correct

### KNOWLEDGE GRAPH
After saving related observations, use \`cortex_relate\` to connect them:
- references, relates_to, follows, supersedes, contradicts
Use \`cortex_graph\` to explore connections from any observation.
Use \`cortex_score\` to check/recalculate observation importance.

### SEARCH & RETRIEVAL

When the user asks to recall something — "remember", "recall", "what did we do":
1. First call \`cortex_context\` — checks recent session history (fast)
2. If not found, call \`cortex_search\` with relevant keywords (FTS5)
3. If still not found, try \`cortex_search_hybrid\` for FTS5 + vector combined search
4. If you find a match, use \`cortex_get_observation\` for full content (search returns 300-char previews only)

Also search memory PROACTIVELY when:
- Starting work on something that might have done before
- The user mentions a topic you have no context on
- The user's FIRST message references the project

### REVISION HISTORY & TIMELINE
- \`cortex_revision_history(observation_id)\` — see how an observation evolved across upserts
- \`cortex_timeline(observation_id, before, after)\` — chronological context around an observation
- Use these when an artifact seems stale or when auditing changes

### PROJECT HYGIENE
- If project name fragmented: \`cortex_merge_projects(from: "variant1,variant2", to: "canonical")\`
- To archive obsolete observations: \`cortex_archive(observation_id)\`
- To permanently delete: \`cortex_delete(id, hard_delete: true)\`

### SESSION CLOSE PROTOCOL (mandatory)

Before ending a session or saying "done":
1. Call \`cortex_session_summary\` with: Goal, Discoveries, Accomplished, Next Steps, Relevant Files.
This is NOT optional. If you skip this, the next session starts blind.

### AFTER COMPACTION

If you see a message about compaction or context reset:
1. IMMEDIATELY call \`cortex_session_summary\` with the compacted summary content
2. Then call \`cortex_context\` to recover context from previous sessions
3. Use \`cortex_search_hybrid\` if more detail needed
4. Only THEN continue working
`

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

// The persisted Session echo is validated by identity: the server returns the
// session that was sent (optional omitempty fields are not asserted).
function persistedSession(sent: {
  id: string
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
  // persisted echo of the exact session identity, or an explicit 409
  // conflict proving it already exists. Failures are classified, never
  // cached, and a later event retries instead of assuming continuity.
  async function ensureSession(sessionId: string): Promise<SessionEnsureResult> {
    if (!sessionId || subAgentSessions.has(sessionId)) {
      return { confirmed: false, classification: "validation" }
    }
    if (knownSessions.has(sessionId)) {
      return { confirmed: true, classification: "success" }
    }
    if (!CORTEX_HTTP_TOKEN) {
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
      knownSessions.add(sessionId)
      return { confirmed: true, classification: "success" }
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
          await ensureSession(sessionId)
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
        if (sessionId) {
          await ensureSession(sessionId)
          toolCounts.set(sessionId, (toolCounts.get(sessionId) ?? 0) + 1)
        }

        // Passive capture from Task tool output
        if (input.tool === "Task" && output && sessionId) {
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
      if (output.system.length > 0) {
        output.system[output.system.length - 1] += "\n\n" + MEMORY_INSTRUCTIONS
      } else {
        output.system.push(MEMORY_INSTRUCTIONS)
      }
    },

    // ─── Compaction Hook ──────────────────────────────────────────

    "experimental.session.compacting": async (input, output) => {
      if (input.sessionID) {
        await ensureSession(input.sessionID)
      }

      if (CORTEX_HTTP_TOKEN) {
        const result = await request(
          `/api/observations?project=${encodeURIComponent(project)}&limit=20`
        )
        if (!result.ok) {
          report("observation", result.classification)
        } else if (!Array.isArray(result.body)) {
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
    },
  }
}
