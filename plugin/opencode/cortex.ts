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
 */

import type { Plugin } from "@opencode-ai/plugin"

// ─── Configuration ───────────────────────────────────────────────────────────

const CORTEX_HTTP_PORT = parseInt(process.env.CORTEX_HTTP_PORT ?? "7438")
const CORTEX_URL = `http://127.0.0.1:${CORTEX_HTTP_PORT}`
const CORTEX_BIN = process.env.CORTEX_BIN ?? (() => {
  // Try Bun.which for PATH lookup, fall back to bare command
  try {
    const which = (globalThis as any).Bun?.which?.("cortex")
    if (which) return which
  } catch {}
  return "cortex"
})()

// Cortex's own MCP tools — don't count these as "tool calls" for session stats
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
- Starting work on something that might have been done before
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

// ─── HTTP Client ─────────────────────────────────────────────────────────────

async function cortexFetch(
  path: string,
  opts: { method?: string; body?: any } = {}
): Promise<any> {
  try {
    const res = await fetch(`${CORTEX_URL}${path}`, {
      method: opts.method ?? "GET",
      headers: opts.body ? { "Content-Type": "application/json" } : undefined,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    })
    return await res.json()
  } catch {
    return null
  }
}

async function isCortexRunning(): Promise<boolean> {
  try {
    const res = await fetch(`${CORTEX_URL}/health`, {
      signal: AbortSignal.timeout(500),
    })
    return res.ok
  } catch {
    return false
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

function truncate(str: string, max: number): string {
  if (!str) return ""
  return str.length > max ? str.slice(0, max) + "..." : str
}

function stripPrivateTags(str: string): string {
  if (!str) return ""
  return str.replace(/<private>[\s\S]*?<\/private>/gi, "[REDACTED]").trim()
}

// ─── Plugin Export ───────────────────────────────────────────────────────────

export const Cortex: Plugin = async (ctx) => {
  const project = extractProjectName(ctx.directory)

  const toolCounts = new Map<string, number>()
  const knownSessions = new Set<string>()
  const subAgentSessions = new Set<string>()

  async function ensureSession(sessionId: string): Promise<void> {
    if (!sessionId || knownSessions.has(sessionId)) return
    if (subAgentSessions.has(sessionId)) return
    knownSessions.add(sessionId)
    await cortexFetch("/api/sessions", {
      method: "POST",
      body: {
        id: sessionId,
        project,
        directory: ctx.directory,
      },
    })
  }

  // Try to start cortex server if not running
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
            await cortexFetch(`/api/sessions/${encodeURIComponent(sessionId)}/end`, {
              method: "POST",
              body: { summary: "" },
            })
          }
          toolCounts.delete(sessionId)
          knownSessions.delete(sessionId)
          subAgentSessions.delete(sessionId)
        }
      }
    },

    // ─── User Prompt Capture ──────────────────────────────────────

    "chat.message": async (input, output) => {
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
        await cortexFetch("/api/prompts", {
          method: "POST",
          body: {
            session_id: sessionId,
            content: stripPrivateTags(truncate(finalContent, 2000)),
            project,
          },
        })
      }
    },

    // ─── Tool Execution Hook ─────────────────────────────────────

    "tool.execute.after": async (input, output) => {
      if (CORTEX_TOOLS.has(input.tool.toLowerCase())) return

      const sessionId = input.sessionID
      if (sessionId) {
        await ensureSession(sessionId)
        toolCounts.set(sessionId, (toolCounts.get(sessionId) ?? 0) + 1)
      }

      // Passive capture from Task tool output
      if (input.tool === "Task" && output && sessionId) {
        const text = typeof output === "string" ? output : JSON.stringify(output)
        if (text.length > 50) {
          await cortexFetch("/api/observations", {
            method: "POST",
            body: {
              session_id: sessionId,
              title: "Passive capture from task",
              content: stripPrivateTags(text),
              type: "passive",
              project,
              scope: "project",
            },
          })
        }
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

      // Inject context from previous sessions
      const data = await cortexFetch(
        `/api/observations?project=${encodeURIComponent(project)}&limit=20`
      )
      if (data && Array.isArray(data) && data.length > 0) {
        const ctx = data.map((o: any) => `- [${o.type}] ${o.title}`).join("\n")
        output.context.push(`Recent Cortex memories for ${project}:\n${ctx}`)
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
