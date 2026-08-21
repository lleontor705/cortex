import { afterEach, describe, expect, it, vi } from "vitest"

const TOKEN_CANARY = "token-canary-do-not-log"
const PAYLOAD_CANARY = "payload-canary-do-not-log"
const encoder = new TextEncoder()

type FetchFixture =
  | { kind: "response"; status: number; body: string }
  | { kind: "sequence"; responses: Array<{ status: number; body: string }> }
  | { kind: "transport" }
  | { kind: "timeout" }
  | { kind: "pending" }

// Mirrors the routes registered by internal/http/server.go. In particular,
// cortex_handoff is an MCP tool capability, not an HTTP route on this server.
const AVAILABLE_SERVER_ROUTES = [
  "/health",
  "/api/sessions",
  "/api/prompts",
  "/api/observations",
] as const

type RecordedRequest = {
  url: string
  init?: RequestInit
}

// Mirrors the 201 echo returned by POST /api/sessions on the real Go server:
// the persisted domain.Session identity for what the plugin sent.
const VALID_SESSION = {
  id: "session-1",
  project: "project",
  directory: "/workspace/project",
  started_at: "2026-08-12T00:00:00Z",
}

function jsonResponse(status: number, value: unknown): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

function plainResponse(fixture: { status: number; body: string }): Response {
  return new Response(fixture.body, {
    status: fixture.status,
    headers: { "Content-Type": "application/json" },
  })
}

// Shared fixture dispatch for auxiliary endpoints (sessions, session end,
// protected GET). The delivery path keeps its own dispatch because only it
// supports the pending/abort fixture.
async function dispatchFixture(target: FetchFixture): Promise<Response> {
  if (target.kind === "transport") throw new Error("transport failed")
  if (target.kind === "timeout") throw new DOMException("timed out", "TimeoutError")
  if (target.kind === "sequence") {
    const response = target.responses.shift()
    if (!response) throw new Error("unexpected request")
    return plainResponse(response)
  }
  if (target.kind === "response") return plainResponse(target)
  throw new Error("unsupported fixture")
}

async function createHarness(
  fixture: FetchFixture,
  token = TOKEN_CANARY,
  sessionsFixture?: FetchFixture,
  endFixture?: FetchFixture,
  getFixture?: FetchFixture,
  sessionGetFixture?: FetchFixture,
) {
  vi.resetModules()
  vi.stubEnv("CORTEX_HTTP_TOKEN", token)
  vi.stubEnv("CORTEX_HTTP_PORT", "7438")

  const requests: RecordedRequest[] = []
  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input)
    requests.push({ url, init })

    if (url.endsWith("/health")) return jsonResponse(200, { status: "ok" })
    if (url.includes("/api/observations?")) {
      if (!getFixture) return jsonResponse(200, [])
      return await dispatchFixture(getFixture)
    }
    if (url.endsWith("/end")) {
      if (!endFixture) return jsonResponse(200, { status: "ended" })
      return await dispatchFixture(endFixture)
    }
    if (url.endsWith("/api/sessions")) {
      if (!sessionsFixture) return jsonResponse(201, VALID_SESSION)
      return await dispatchFixture(sessionsFixture)
    }
    if (url.includes("/api/sessions/")) {
      if (!sessionGetFixture) return jsonResponse(200, VALID_SESSION)
      return await dispatchFixture(sessionGetFixture)
    }
    const isDelivery = url.endsWith("/api/prompts") || url.endsWith("/api/observations") || url.includes("/api/handoffs")
    if (!isDelivery) return jsonResponse(200, {})

    if (fixture.kind === "transport") throw new Error("transport failed")
    if (fixture.kind === "timeout") throw new DOMException("timed out", "TimeoutError")
    if (fixture.kind === "pending") {
      return await new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")))
      })
    }
    if (fixture.kind === "sequence") {
      const response = fixture.responses.shift()
      if (!response) throw new Error("unexpected request")
      return new Response(response.body, {
        status: response.status,
        headers: { "Content-Type": "application/json" },
      })
    }
    return new Response(fixture.body, {
      status: fixture.status,
      headers: { "Content-Type": "application/json" },
    })
  })
  vi.stubGlobal("fetch", fetchMock)
  vi.stubGlobal("Bun", {
    which: vi.fn(() => "cortex"),
    spawn: vi.fn(),
    spawnSync: vi.fn(() => ({ exitCode: 1 })),
  })

  const info = vi.spyOn(console, "info").mockImplementation(() => {})
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
  const error = vi.spyOn(console, "error").mockImplementation(() => {})
  const { Cortex } = await import("./cortex")
  const hooks = await Cortex({ directory: "/workspace/project" } as never)

  async function deliver(content = `safe prompt ${PAYLOAD_CANARY}`) {
    await expect(
      hooks["chat.message"]?.(
        { sessionID: "session-1" } as never,
        {
          parts: [{ type: "text", text: content }],
          message: {},
        } as never,
      ),
    ).resolves.toBeUndefined()
  }

  async function deliverPassive(content: string) {
    await expect(
      hooks["tool.execute.after"]?.(
        { sessionID: "session-1", tool: "Task" } as never,
        content as never,
      ),
    ).resolves.toBeUndefined()
  }

  async function emitSessionDeleted() {
    await expect(
      hooks.event?.({
        event: { type: "session.deleted", properties: { info: { id: "session-1" } } },
      } as never),
    ).resolves.toBeUndefined()
  }

  async function compact() {
    const output = { context: [] as string[] }
    await expect(
      hooks["experimental.session.compacting"]?.(
        { sessionID: "session-1" } as never,
        output as never,
      ),
    ).resolves.toBeUndefined()
    return output.context
  }

  async function deliverDurable(content: string, callID = "call-1") {
    await expect(
      hooks["tool.execute.after"]?.(
        { sessionID: "session-1", tool: "cortex_handoff", callID } as never,
        content as never,
      ),
    ).resolves.toBeUndefined()
  }

  function promptRequest(): RecordedRequest | undefined {
    return requests.find((request) => request.url.endsWith("/api/prompts"))
  }

  function passiveRequest(): RecordedRequest | undefined {
    return requests.find((request) => request.url.endsWith("/api/observations"))
  }

  function handoffRequests(): RecordedRequest[] {
    return requests.filter((request) => request.url.includes("/api/handoffs"))
  }

  function sessionPosts(): RecordedRequest[] {
    return requests.filter(
      (request) => request.url.endsWith("/api/sessions") && request.init?.method === "POST",
    )
  }

  function sessionGets(): RecordedRequest[] {
    return requests.filter(
      (request) => request.url.includes("/api/sessions/") && !request.url.endsWith("/end"),
    )
  }

  function endRequest(): RecordedRequest | undefined {
    return requests.find((request) => request.url.includes("/end"))
  }

  function healthRequest(): RecordedRequest | undefined {
    return requests.find((request) => request.url.endsWith("/health"))
  }

  function observationsQuery(): RecordedRequest | undefined {
    return requests.find((request) => request.url.includes("/api/observations?"))
  }

  function output(): string {
    return [...info.mock.calls, ...warn.mock.calls, ...error.mock.calls]
      .flat()
      .map(String)
      .join("\n")
  }

  return {
    compact,
    deliver,
    deliverDurable,
    deliverPassive,
    emitSessionDeleted,
    endRequest,
    handoffRequests,
    healthRequest,
    observationsQuery,
    output,
    passiveRequest,
    promptRequest,
    requests,
    sessionGets,
    sessionPosts,
  }
}

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllEnvs()
  vi.unstubAllGlobals()
})

describe("ORACLE-PLUGIN-001 OpenCode credentials, redaction, and continuity", () => {
  it("uses the installed CORTEX_HTTP_TOKEN environment credential without exposing it", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })

    await harness.deliver()

    const request = harness.promptRequest()
    expect(request).toBeDefined()
    expect(new Headers(request?.init?.headers).get("Authorization")).toBe(`Bearer ${TOKEN_CANARY}`)
    expect(harness.output()).not.toContain(TOKEN_CANARY)
  })

  it("reports missing configuration without authenticating, claiming success, or blocking the host", async () => {
    const harness = await createHarness(
      {
        kind: "response",
        status: 201,
        body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
      },
      "",
    )

    await harness.deliver()

    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.output()).toContain("config")
    expect(harness.output()).not.toContain("success")
  })

  for (const [status, classification] of [
    [401, "unauthorized"],
    [403, "forbidden"],
  ] as const) {
    it(`classifies ${status} as ${classification}, redacts canaries, and remains nonblocking`, async () => {
      const harness = await createHarness({
        kind: "response",
        status,
        body: JSON.stringify({ error: `${TOKEN_CANARY} ${PAYLOAD_CANARY}` }),
      })

      await harness.deliver()

      expect(harness.output()).toContain(classification)
      expect(harness.output()).not.toContain("success")
      expect(harness.output()).not.toContain(TOKEN_CANARY)
      expect(harness.output()).not.toContain(PAYLOAD_CANARY)
    })
  }
})

describe("ORACLE-PLUGIN-002 OpenCode HTTP outcome matrix", () => {
  const failures: Array<[string, FetchFixture, string]> = [
    ["409", { kind: "response", status: 409, body: JSON.stringify({ error: "conflict" }) }, "conflict"],
    ["500", { kind: "response", status: 500, body: JSON.stringify({ error: "failed" }) }, "unavailable"],
    ["timeout", { kind: "timeout" }, "timeout"],
    ["transport error", { kind: "transport" }, "unavailable"],
    ["invalid body", { kind: "response", status: 200, body: "not-json" }, "invalid_response"],
  ]

  it("does not accept a structured handoff confirmation from the prompt endpoint", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 7 } }),
    })

    await harness.deliver()

    expect(harness.output()).toContain("invalid_response")
    expect(harness.output()).not.toContain("success")
    expect(harness.output()).not.toContain(TOKEN_CANARY)
    expect(harness.output()).not.toContain(PAYLOAD_CANARY)
  })

  it("accepts the current prompt endpoint persisted Prompt response", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({
        id: 41,
        content: `safe prompt ${PAYLOAD_CANARY}`,
        project: "project",
        session_id: "session-1",
        created_at: "2026-08-12T00:00:00Z",
      }),
    })

    await harness.deliver()

    expect(harness.output().match(/success/g)).toHaveLength(1)
  })

  it("accepts the current observation endpoint persisted Observation response", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({
        id: 42,
        title: "Passive capture from task",
        content: "x".repeat(60),
        type: "passive",
        project: "project",
        scope: "project",
        session_id: "session-1",
        topic_key: "",
        confidence: 0,
        source: "",
        created_at: "2026-08-12T00:00:00Z",
        updated_at: "2026-08-12T00:00:00Z",
      }),
    })

    await harness.deliverPassive("x".repeat(60))

    expect(harness.output().match(/success/g)).toHaveLength(1)
  })

  for (const [name, fixture, deliver] of [
    [
      "prompt endpoint returning an Observation",
      {
        id: 42,
        title: "Passive capture from task",
        content: `safe prompt ${PAYLOAD_CANARY}`,
        type: "passive",
        project: "project",
        scope: "project",
        session_id: "session-1",
        topic_key: "",
        confidence: 0,
        source: "",
        created_at: "2026-08-12T00:00:00Z",
        updated_at: "2026-08-12T00:00:00Z",
      },
      "prompt",
    ],
    [
      "observation endpoint returning a Prompt",
      {
        id: 41,
        content: "x".repeat(60),
        project: "project",
        session_id: "session-1",
        created_at: "2026-08-12T00:00:00Z",
      },
      "observation",
    ],
    [
      "persisted object with mismatched identity",
      {
        id: 41,
        content: "different prompt",
        project: "project",
        session_id: "session-1",
        created_at: "2026-08-12T00:00:00Z",
      },
      "prompt",
    ],
  ] as const) {
    it(`rejects ${name}`, async () => {
      const harness = await createHarness({ kind: "response", status: 201, body: JSON.stringify(fixture) })

      if (deliver === "prompt") await harness.deliver()
      else await harness.deliverPassive("x".repeat(60))

      expect(harness.output()).toContain("invalid_response")
      expect(harness.output()).not.toContain("success")
    })
  }

  for (const [name, body] of [
    ["malformed UUID", { status: "created", observation_ref: { public_id: "not-a-uuid" } }],
    ["both refs", { status: "created", observation_ref: { local_id: 1, public_id: "123e4567-e89b-42d3-a456-426614174000" } }],
    ["neither ref", { status: "created", observation_ref: {} }],
    ["unknown status", { status: "stored", observation_ref: { local_id: 1 } }],
    ["extra confirmation property", { status: "created", observation_ref: { local_id: 1 }, extra: true }],
    ["extra ref property", { status: "created", observation_ref: { local_id: 1, extra: true } }],
  ] as const) {
    it(`rejects structured confirmation with ${name}`, async () => {
      const harness = await createHarness({ kind: "response", status: 201, body: JSON.stringify(body) })

      await harness.deliver()

      expect(harness.output()).toContain("invalid_response")
      expect(harness.output()).not.toContain("success")
    })
  }

  it("does not accept a structured handoff confirmation from the observation endpoint", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })

    await harness.deliverPassive("x".repeat(60))

    expect(harness.output()).toContain("invalid_response")
    expect(harness.output()).not.toContain("success")
  })

  it("rejects an exact public UUID structured confirmation from the prompt endpoint", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({
        status: "updated",
        observation_ref: { public_id: "123e4567-e89b-42d3-a456-426614174000" },
      }),
    })

    await harness.deliver()

    expect(harness.output()).toContain("invalid_response")
    expect(harness.output()).not.toContain("success")
  })

  it("aborts a fetch that remains pending at the bounded deadline under fake timers", async () => {
    vi.useFakeTimers()
    const harness = await createHarness({ kind: "pending" })

    const delivery = harness.deliver()
    await vi.advanceTimersByTimeAsync(1999)
    expect(harness.output()).not.toContain("timeout")
    await vi.advanceTimersByTimeAsync(1)
    await delivery

    expect(harness.output().match(/timeout/g)).toHaveLength(1)
    expect(harness.output()).not.toContain("success")
    vi.useRealTimers()
  })

  for (const [name, fixture, classification] of failures) {
    it(`treats ${name} as ${classification} with zero false success and returns control`, async () => {
      const harness = await createHarness(fixture)

      await harness.deliver()

      expect(harness.output()).toContain(classification)
      expect(harness.output()).not.toContain("success")
      expect(harness.output()).not.toContain(TOKEN_CANARY)
      expect(harness.output()).not.toContain(PAYLOAD_CANARY)
    })
  }
})

describe("ORACLE-PLUGIN-003 OpenCode UTF-8 truncation metrics", () => {
  // cortex_handoff is an MCP capability: its result was already delivered by
  // the MCP channel. The plugin must not interpret, redeliver, or classify
  // that result — doing so fabricates delivery signals from tool output.
  it("ignores a valid cortex_handoff MCP result without any delivery signal or HTTP call", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 9 } }),
    })
    const mcpResult = JSON.stringify({
      status: "created",
      observation_ref: { local_id: 9 },
      idempotency_key: "key-1",
    })

    await harness.deliverDurable(mcpResult)

    expect(AVAILABLE_SERVER_ROUTES).not.toContain("/api/handoffs")
    expect(harness.requests).toHaveLength(1) // the startup /health probe only
    expect(harness.output()).toBe("")
    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.passiveRequest()).toBeUndefined()
  })

  it("stays neutral across handoff result shapes, repeated calls, and malformed output", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })

    await harness.deliverDurable(JSON.stringify({ truncated: true, content: "lossy" }), "truncated")
    await harness.deliverDurable("plain text result", "fallback")
    await harness.deliverDurable(
      JSON.stringify({ idempotency_key: "same", observation: { content: PAYLOAD_CANARY } }),
      "same",
    )
    await harness.deliverDurable(
      JSON.stringify({ idempotency_key: "same", observation: { content: PAYLOAD_CANARY } }),
      "same",
    )

    expect(harness.handoffRequests()).toHaveLength(0)
    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.passiveRequest()).toBeUndefined()
    expect(harness.output()).toBe("")
  })

  it("reports consistent byte metrics for an oversized ASCII prompt", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })
    const original = "a".repeat(2100)

    await harness.deliver(original)

    const body = JSON.parse(String(harness.promptRequest()?.init?.body))
    expect(body.truncated).toBe(true)
    expect(body.original_bytes).toBe(encoder.encode(original).byteLength)
    expect(body.stored_bytes).toBe(encoder.encode(body.content).byteLength)
    expect(body.stored_bytes).toBeLessThanOrEqual(2000)
  })

  it("preserves complete Unicode runes and reports actual UTF-8 bytes at the boundary", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })
    const original = `${"a".repeat(1999)}💾${PAYLOAD_CANARY}`

    await harness.deliver(original)

    const body = JSON.parse(String(harness.promptRequest()?.init?.body))
    const roundTrip = new TextDecoder("utf-8", { fatal: true }).decode(encoder.encode(body.content))
    expect(roundTrip).toBe(body.content)
    expect(body.content).not.toContain("�")
    expect(body.truncated).toBe(true)
    expect(body.original_bytes).toBe(encoder.encode(original).byteLength)
    expect(body.stored_bytes).toBe(encoder.encode(body.content).byteLength)
    expect(body.stored_bytes).toBeLessThanOrEqual(2000)
  })

  it("reports consistent byte metrics for oversized passive ASCII capture", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })
    const original = "b".repeat(2100)

    await harness.deliverPassive(original)

    const body = JSON.parse(String(harness.passiveRequest()?.init?.body))
    expect(body.truncated).toBe(true)
    expect(body.original_bytes).toBe(encoder.encode(original).byteLength)
    expect(body.stored_bytes).toBe(encoder.encode(body.content).byteLength)
    expect(body.stored_bytes).toBeLessThanOrEqual(2000)
  })

  it("keeps passive Unicode capture valid at a multibyte boundary", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ status: "created", observation_ref: { local_id: 1 } }),
    })
    const original = `${"界".repeat(667)}💾${PAYLOAD_CANARY}`

    await harness.deliverPassive(original)

    const body = JSON.parse(String(harness.passiveRequest()?.init?.body))
    const roundTrip = new TextDecoder("utf-8", { fatal: true }).decode(encoder.encode(body.content))
    expect(roundTrip).toBe(body.content)
    expect(body.content).not.toContain("�")
    expect(body.truncated).toBe(true)
    expect(body.original_bytes).toBe(encoder.encode(original).byteLength)
    expect(body.stored_bytes).toBe(encoder.encode(body.content).byteLength)
    expect(body.stored_bytes).toBeLessThanOrEqual(2000)
  })
})

describe("ORACLE-PLUGIN-004 protected GET and ensureSession result contract", () => {
  it("attaches the bearer credential to the protected observations GET", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ id: 41, content: "x", project: "project", session_id: "session-1", created_at: "t" }),
    })

    await harness.compact()

    const query = harness.observationsQuery()
    expect(query).toBeDefined()
    expect(new Headers(query?.init?.headers).get("Authorization")).toBe(`Bearer ${TOKEN_CANARY}`)
  })

  it("keeps /health as the only unauthenticated request", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ id: 41, content: "x", project: "project", session_id: "session-1", created_at: "t" }),
    })

    await harness.compact()

    const health = harness.healthRequest()
    expect(health).toBeDefined()
    expect(new Headers(health?.init?.headers).get("Authorization")).toBeNull()
  })

  it("caches the session only after a confirmed persisted-session echo", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({
        id: 41,
        content: `safe prompt ${PAYLOAD_CANARY}`,
        project: "project",
        session_id: "session-1",
        created_at: "2026-08-12T00:00:00Z",
      }),
    })

    await harness.deliver()
    await harness.deliver()

    expect(harness.sessionPosts()).toHaveLength(1)
  })

  it("does not cache a failed session create and classifies each failure", async () => {
    const harness = await createHarness(
      {
        kind: "response",
        status: 201,
        body: JSON.stringify({
          id: 41,
          content: `safe prompt ${PAYLOAD_CANARY}`,
          project: "project",
          session_id: "session-1",
          created_at: "2026-08-12T00:00:00Z",
        }),
      },
      TOKEN_CANARY,
      { kind: "response", status: 500, body: JSON.stringify({ error: "failed" }) },
    )

    await harness.deliver()
    await harness.deliver()

    expect(harness.sessionPosts()).toHaveLength(2)
    expect(harness.output().match(/session delivery unavailable/g)).toHaveLength(2)
    expect(harness.output()).not.toContain(TOKEN_CANARY)
  })

  it("does not cache a session echo with the wrong persisted identity", async () => {
    const harness = await createHarness(
      {
        kind: "response",
        status: 201,
        body: JSON.stringify({
          id: 41,
          content: `safe prompt ${PAYLOAD_CANARY}`,
          project: "project",
          session_id: "session-1",
          created_at: "2026-08-12T00:00:00Z",
        }),
      },
      TOKEN_CANARY,
      { kind: "response", status: 201, body: JSON.stringify({ id: "session-1" }) },
    )

    await harness.deliver()
    await harness.deliver()

    expect(harness.sessionPosts()).toHaveLength(2)
    expect(harness.output().match(/session delivery invalid_response/g)).toHaveLength(2)
  })

  it("does not cache an unauthorized session create", async () => {
    const harness = await createHarness(
      {
        kind: "response",
        status: 201,
        body: JSON.stringify({
          id: 41,
          content: `safe prompt ${PAYLOAD_CANARY}`,
          project: "project",
          session_id: "session-1",
          created_at: "2026-08-12T00:00:00Z",
        }),
      },
      TOKEN_CANARY,
      { kind: "response", status: 401, body: JSON.stringify({ error: "no token" }) },
    )

    await harness.deliver()
    await harness.deliver()

    expect(harness.sessionPosts()).toHaveLength(2)
    expect(harness.output().match(/session delivery unauthorized/g)).toHaveLength(2)
  })

  it("confirms a 409 conflict only through the exact persisted read-back echo", async () => {
    const harness = await createHarness(
      {
        kind: "response",
        status: 201,
        body: JSON.stringify({
          id: 41,
          content: `safe prompt ${PAYLOAD_CANARY}`,
          project: "project",
          session_id: "session-1",
          created_at: "2026-08-12T00:00:00Z",
        }),
      },
      TOKEN_CANARY,
      { kind: "sequence", responses: [{ status: 409, body: JSON.stringify({ error: "exists" }) }] },
      undefined,
      undefined,
      { kind: "response", status: 200, body: JSON.stringify(VALID_SESSION) },
    )

    await harness.deliver()
    await harness.deliver()
    await harness.emitSessionDeleted()

    expect(harness.sessionPosts()).toHaveLength(1)
    expect(harness.sessionGets()).toHaveLength(1)
    const request = harness.promptRequest()
    expect(request).toBeDefined()
    expect(new Headers(request?.init?.headers).get("Authorization")).toBe(`Bearer ${TOKEN_CANARY}`)
    expect(harness.endRequest()).toBeDefined()
  })

  it("ends only a session whose create was actually confirmed", async () => {
    const promptEcho = {
      kind: "response",
      status: 201,
      body: JSON.stringify({
        id: 41,
        content: `safe prompt ${PAYLOAD_CANARY}`,
        project: "project",
        session_id: "session-1",
        created_at: "2026-08-12T00:00:00Z",
      }),
    } as const

    const failed = await createHarness(promptEcho, TOKEN_CANARY, { kind: "transport" })
    await failed.deliver()
    await failed.emitSessionDeleted()
    expect(failed.endRequest()).toBeUndefined()

    const confirmed = await createHarness(promptEcho)
    await confirmed.deliver()
    await confirmed.emitSessionDeleted()
    expect(confirmed.endRequest()).toBeDefined()
    expect(new Headers(confirmed.endRequest()?.init?.headers).get("Authorization")).toBe(
      `Bearer ${TOKEN_CANARY}`,
    )
  })

  it("rejects empty and cross-endpoint 2xx bodies on the observation endpoint", async () => {
    for (const body of [{}, { status: "ended" }]) {
      const harness = await createHarness({
        kind: "response",
        status: 201,
        body: JSON.stringify(body),
      })

      await harness.deliverPassive("x".repeat(60))

      expect(harness.output()).toContain("invalid_response")
      expect(harness.output()).not.toContain("success")
    }
  })
})

describe("ORACLE-PLUGIN-005 review fixes: exact session-end body, additive observation fields, GET failure classification", () => {
  const promptEcho = {
    kind: "response",
    status: 201,
    body: JSON.stringify({
      id: 41,
      content: `safe prompt ${PAYLOAD_CANARY}`,
      project: "project",
      session_id: "session-1",
      created_at: "2026-08-12T00:00:00Z",
    }),
  } as const

  async function endSession(endBody: unknown) {
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      undefined,
      { kind: "response", status: 200, body: JSON.stringify(endBody) },
    )
    await harness.deliver()
    await harness.emitSessionDeleted()
    return harness
  }

  it("accepts the exact single-key ended-status body from the session-end endpoint", async () => {
    const harness = await endSession({ status: "ended" })

    expect(harness.endRequest()).toBeDefined()
    expect(harness.output().match(/session delivery success/g)).toHaveLength(1)
  })

  for (const [name, body] of [
    ["an empty object", {}],
    ["a wrong status", { status: "running" }],
    ["extra keys", { status: "ended", extra: true }],
    ["an ended body from another shape", { status: "ended", id: 7, summary: "" }],
  ] as const) {
    it(`rejects SessionStop 2xx ${name}`, async () => {
      const harness = await endSession(body)

      expect(harness.output().match(/session delivery invalid_response/g)).toHaveLength(1)
      expect(harness.output()).not.toMatch(/session delivery success/)
      expect(harness.output()).not.toContain(TOKEN_CANARY)
      expect(harness.output()).not.toContain(PAYLOAD_CANARY)
    })
  }

  const persistedObservationBase = {
    id: 42,
    title: "Passive capture from task",
    content: "x".repeat(60),
    type: "passive",
    project: "project",
    scope: "project",
    session_id: "session-1",
    topic_key: "",
    confidence: 0,
    source: "",
    created_at: "2026-08-12T00:00:00Z",
    updated_at: "2026-08-12T00:00:00Z",
  }

  it("accepts a persisted observation carrying additive server fields like tags", async () => {
    const harness = await createHarness({
      kind: "response",
      status: 201,
      body: JSON.stringify({ ...persistedObservationBase, tags: ["late"], sync_id: "sync-9" }),
    })

    await harness.deliverPassive("x".repeat(60))

    expect(harness.output().match(/success/g)).toHaveLength(1)
  })

  for (const [name, mutation] of [
    ["a non-positive id", (o: Record<string, unknown>) => ({ ...o, id: 0 })],
    ["a missing sent field", (o: Record<string, unknown>) => {
      const copy = { ...o }
      delete copy.title
      return copy
    }],
    ["a mismatched sent field", (o: Record<string, unknown>) => ({ ...o, project: "other" })],
  ] as const) {
    it(`rejects a persisted observation with ${name} even when additive fields are allowed`, async () => {
      const harness = await createHarness({
        kind: "response",
        status: 201,
        body: JSON.stringify({ ...mutation({ ...persistedObservationBase }), tags: ["late"] }),
      })

      await harness.deliverPassive("x".repeat(60))

      expect(harness.output()).toContain("invalid_response")
      expect(harness.output()).not.toContain("success")
    })
  }

  for (const [name, getFixture, classification] of [
    ["401", { kind: "response", status: 401, body: JSON.stringify({ error: TOKEN_CANARY }) }, "unauthorized"],
    ["403", { kind: "response", status: 403, body: JSON.stringify({ error: "forbidden" }) }, "forbidden"],
    ["5xx", { kind: "response", status: 500, body: JSON.stringify({ error: "failed" }) }, "unavailable"],
    ["timeout", { kind: "timeout" }, "timeout"],
    ["transport failure", { kind: "transport" }, "unavailable"],
    ["a non-array 2xx body", { kind: "response", status: 200, body: JSON.stringify({ status: "ended" }) }, "invalid_response"],
    ["a not-json 2xx body", { kind: "response", status: 200, body: "not-json" }, "invalid_response"],
  ] as const) {
    it(`classifies protected observations GET ${name} as ${classification}`, async () => {
      const harness = await createHarness(promptEcho, TOKEN_CANARY, undefined, undefined, getFixture)

      await harness.compact()

      expect(harness.output()).toContain(`observation delivery ${classification}`)
      expect(harness.output()).not.toContain(TOKEN_CANARY)
      expect(harness.output()).not.toContain(PAYLOAD_CANARY)
    })
  }

  it("does not emit a failure classification for a valid observations list", async () => {
    const harness = await createHarness(promptEcho, TOKEN_CANARY, undefined, undefined, {
      kind: "response",
      status: 200,
      body: JSON.stringify([]),
    })

    await harness.compact()

    expect(harness.output()).toBe("")
  })
})

describe("ORACLE-PLUGIN-006 SEC-04 auth-gated writes and confirmed-session context", () => {
  const promptEcho = {
    kind: "response",
    status: 201,
    body: JSON.stringify({
      id: 41,
      content: `safe prompt ${PAYLOAD_CANARY}`,
      project: "project",
      session_id: "session-1",
      created_at: "2026-08-12T00:00:00Z",
    }),
  } as const

  const observationEcho = {
    kind: "response",
    status: 201,
    body: JSON.stringify({
      id: 42,
      title: "Passive capture from task",
      content: "x".repeat(60),
      type: "passive",
      project: "project",
      scope: "project",
      session_id: "session-1",
      topic_key: "",
      confidence: 0,
      source: "",
      created_at: "2026-08-12T00:00:00Z",
      updated_at: "2026-08-12T00:00:00Z",
    }),
  } as const

  const conflictTwice: FetchFixture = {
    kind: "sequence",
    responses: [
      { status: 409, body: JSON.stringify({ error: "exists" }) },
      { status: 409, body: JSON.stringify({ error: "exists" }) },
    ],
  }

  it("sends zero prompt writes when the session create fails", async () => {
    const harness = await createHarness(promptEcho, TOKEN_CANARY, { kind: "transport" })

    await harness.deliver()

    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.output()).toContain("session delivery unavailable")
    expect(harness.output()).not.toContain("success")
    expect(harness.output()).not.toContain(TOKEN_CANARY)
    expect(harness.output()).not.toContain(PAYLOAD_CANARY)
  })

  it("sends zero passive observation writes when the session create fails", async () => {
    const harness = await createHarness(
      observationEcho,
      TOKEN_CANARY,
      { kind: "response", status: 500, body: JSON.stringify({ error: "failed" }) },
    )

    await harness.deliverPassive("x".repeat(60))

    expect(harness.passiveRequest()).toBeUndefined()
    expect(harness.output()).toContain("session delivery unavailable")
    expect(harness.output()).not.toContain("success")
  })

  it("sends zero prompt writes when the session identity echo mismatches", async () => {
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      { kind: "response", status: 201, body: JSON.stringify({ ...VALID_SESSION, project: "sibling" }) },
    )

    await harness.deliver()

    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.output()).toContain("session delivery invalid_response")
  })

  it("does not confirm a 409 conflict when the read-back identity mismatches", async () => {
    const mismatchedRead: FetchFixture = {
      kind: "response",
      status: 200,
      body: JSON.stringify({ ...VALID_SESSION, project: "sibling-project" }),
    }
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      conflictTwice,
      undefined,
      undefined,
      mismatchedRead,
    )

    await harness.deliver()
    await harness.deliver()

    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.sessionPosts()).toHaveLength(2)
    expect(harness.sessionGets()).toHaveLength(2)
    expect(harness.output()).toContain("session delivery invalid_response")
    expect(harness.output()).not.toContain("success")
  })

  it("does not confirm a 409 conflict when the read-back request fails", async () => {
    const singleConflict: FetchFixture = {
      kind: "sequence",
      responses: [{ status: 409, body: JSON.stringify({ error: "exists" }) }],
    }
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      singleConflict,
      undefined,
      undefined,
      { kind: "timeout" },
    )

    await harness.deliver()

    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.output()).toContain("session delivery timeout")
    expect(harness.output()).not.toContain("success")
  })

  it("keeps a rejected session unconfirmed and retryable on the next host event", async () => {
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      {
        kind: "sequence",
        responses: [
          { status: 500, body: JSON.stringify({ error: "boom" }) },
          { status: 401, body: JSON.stringify({ error: "no" }) },
        ],
      },
    )

    await harness.deliver()
    await harness.deliver()

    expect(harness.sessionPosts()).toHaveLength(2)
    expect(harness.promptRequest()).toBeUndefined()
    expect(harness.output()).toContain("session delivery unavailable")
    expect(harness.output()).toContain("session delivery unauthorized")
  })

  it("injects no compaction context and issues no protected read without a confirmed session", async () => {
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      { kind: "response", status: 401, body: JSON.stringify({ error: "no token" }) },
    )

    const context = await harness.compact()

    expect(harness.observationsQuery()).toBeUndefined()
    expect(context).toHaveLength(1)
    expect(context.join("\n")).not.toContain("Recent Cortex memories")
    expect(harness.output()).toContain("session delivery unauthorized")
  })

  for (const [name, items] of [
    ["a non-object item", ["not-an-object"]],
    ["an item without a string title", [{ type: "decision" }]],
    ["an item without a string type", [{ title: "T" }]],
  ] as const) {
    it(`rejects compaction context with ${name} without injecting or crashing`, async () => {
      const harness = await createHarness(
        promptEcho,
        TOKEN_CANARY,
        undefined,
        undefined,
        {
          kind: "response",
          status: 200,
          body: JSON.stringify([{ title: "ok", type: "decision" }, ...items]),
        },
      )

      const context = await harness.compact()

      expect(context).toHaveLength(1)
      expect(context.join("\n")).not.toContain("Recent Cortex memories")
      expect(harness.output()).toContain("observation delivery invalid_response")
    })
  }

  it("still injects validated compaction context from a confirmed session", async () => {
    const harness = await createHarness(
      promptEcho,
      TOKEN_CANARY,
      undefined,
      undefined,
      {
        kind: "response",
        status: 200,
        body: JSON.stringify([{ title: "Use zero-downtime deploys", type: "decision" }]),
      },
    )

    const context = await harness.compact()

    expect(context.join("\n")).toContain("Recent Cortex memories")
    expect(context.join("\n")).toContain("- [decision] Use zero-downtime deploys")
  })
})
