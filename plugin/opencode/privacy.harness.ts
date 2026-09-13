import { vi } from "vitest"
export const HARNESS_ENV_KEYS = [
  "CORTEX_HTTP_TOKEN",
  "CORTEX_SERVER_URL",
  "CORTEX_URL",
  "CORTEX_HTTP_PORT",
  "CORTEX_MODE",
  "CORTEX_SYNC_ENABLED",
  "CORTEX_SYNC_URL",
] as const
export interface RecordedRequest { url: string; method: string; headers: Record<string, string>; body?: any }
export interface PrivacyHarnessOptions {
  token?: string; url?: string; serverUrl?: string; mode?: string; directory?: string
  sessionStatus?: number; sessionBody?: unknown; promptStatus?: number; observationStatus?: number
}
export interface PrivacyHarness {
  hooks: Awaited<ReturnType<typeof import("./cortex").Cortex>>; requests: RecordedRequest[]; logs: string[]
  sessionRequests: () => RecordedRequest[]; promptRequests: () => RecordedRequest[]; observationRequests: () => RecordedRequest[]
  deliverPrompt: (text: string, sessionId?: string) => Promise<void>; deliverPassive: (text: string, sessionId?: string, tool?: string) => Promise<void>; restore: () => void
}
const jsonRes = (status: number, data: unknown) =>
  new Response(JSON.stringify(data), { status, headers: { "Content-Type": "application/json" } })

export async function createPrivacyHarness(options: PrivacyHarnessOptions = {}): Promise<PrivacyHarness> {
  const token = options.token ?? "privacy-test-token"
  const directory = options.directory ?? "/workspace/project"
  const initialEnv = new Map<string, string | undefined>()
  for (const key of HARNESS_ENV_KEYS) initialEnv.set(key, process.env[key])
  vi.resetModules()
  vi.stubEnv("CORTEX_HTTP_TOKEN", token)
  if (options.serverUrl !== undefined) {
    vi.stubEnv("CORTEX_SERVER_URL", options.serverUrl)
  } else {
    vi.stubEnv("CORTEX_SERVER_URL", undefined as any)
  }
  vi.stubEnv("CORTEX_URL", options.url ?? "http://127.0.0.1:7438")
  vi.stubEnv("CORTEX_HTTP_PORT", "7438")
  vi.stubEnv("CORTEX_MODE", options.mode ?? "local")
  vi.stubEnv("CORTEX_SYNC_ENABLED", "false")
  vi.stubEnv("CORTEX_SYNC_URL", "")
  const requests: RecordedRequest[] = []
  const logs: string[] = []
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = typeof input === "string" ? input : "url" in input ? input.url : String(input)
    const method = init?.method ?? (typeof input === "object" && "method" in input ? input.method : "GET")
    const headers = (init?.headers as Record<string, string>) ?? {}
    let body: any = undefined
    if (init?.body) {
      try { body = JSON.parse(String(init.body)) } catch { body = init.body }
    }
    requests.push({ url, method, headers, body })
    if (url.endsWith("/health")) return jsonRes(200, { status: "ok" })
    if (url.endsWith("/end")) return jsonRes(200, { status: "ended" })
    if (url.endsWith("/api/sessions")) {
      const status = options.sessionStatus ?? 201
      if (status >= 400) return jsonRes(status, options.sessionBody ?? { error: "session failed" })
      return jsonRes(201, options.sessionBody ?? { id: body?.id ?? "session-1", project: body?.project ?? "project", directory: body?.directory ?? directory, started_at: "2026-09-12T00:00:00Z" })
    }
    if (url.includes("/api/sessions/")) {
      return jsonRes(200, { id: "session-1", project: "project", directory, started_at: "2026-09-12T00:00:00Z" })
    }
    if (url.endsWith("/api/prompts")) {
      const status = options.promptStatus ?? 201
      if (status >= 400) return jsonRes(status, { error: "prompt failed" })
      return jsonRes(201, { id: 1, content: body?.content ?? "", project: body?.project ?? "project", session_id: body?.session_id ?? "session-1", created_at: "2026-09-12T00:00:00Z" })
    }
    if (url.endsWith("/api/observations")) {
      const status = options.observationStatus ?? 201
      if (status >= 400) return jsonRes(status, { error: "observation failed" })
      return jsonRes(201, { id: 1, session_id: body?.session_id ?? "session-1", content: body?.content ?? "", title: body?.title ?? "Passive capture from task", project: body?.project ?? "project", type: body?.type ?? "passive", scope: body?.scope ?? "project", created_at: "2026-09-12T00:00:00Z" })
    }

    const safeUrl = url.replace(/([?&](?:token|api_key|secret)=)[^&]+/gi, "$1[REDACTED]")
    throw new Error(`Unknown fetch endpoint: ${method} ${safeUrl}`)
  })

  vi.stubGlobal("fetch", fetchMock)
  vi.stubGlobal("Bun", { which: vi.fn(() => "cortex"), spawn: vi.fn(), spawnSync: vi.fn(() => ({ exitCode: 1 })) })
  const captureLog = (msg: unknown) => logs.push(String(msg))
  vi.spyOn(console, "info").mockImplementation(captureLog)
  vi.spyOn(console, "warn").mockImplementation(captureLog)
  vi.spyOn(console, "error").mockImplementation(captureLog)
  const { Cortex } = await import("./cortex")
  const hooks = await Cortex({ directory } as never)

  if (!hooks || typeof hooks["chat.message"] !== "function" || typeof hooks["tool.execute.after"] !== "function") {
    throw new Error("Cortex plugin missing required hooks")
  }
  return {
    hooks,
    requests,
    logs,
    sessionRequests: () => requests.filter((r) => r.url.endsWith("/api/sessions")),
    promptRequests: () => requests.filter((r) => r.url.endsWith("/api/prompts")),
    observationRequests: () => requests.filter((r) => r.url.endsWith("/api/observations")),
    deliverPrompt: async (text: string, sessionId = "session-1") => {
      const hook = hooks["chat.message"]
      if (typeof hook !== "function") {
        throw new Error("Missing required hook: chat.message")
      }
      await hook({ sessionID: sessionId } as never, { parts: [{ type: "text", text }], message: {} } as never)
    },
    deliverPassive: async (text: string, sessionId = "session-1", tool = "Task") => {
      const hook = hooks["tool.execute.after"]
      if (typeof hook !== "function") {
        throw new Error("Missing required hook: tool.execute.after")
      }
      await hook({ sessionID: sessionId, tool } as never, text as never)
    },
    restore: () => {
      vi.unstubAllEnvs()
      for (const [key, val] of initialEnv) {
        if (val === undefined) delete process.env[key]
        else process.env[key] = val
      }
      vi.unstubAllGlobals()
      vi.restoreAllMocks()
    },
  }
}
