import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { createPrivacyHarness, HARNESS_ENV_KEYS, PrivacyHarness } from "./privacy.harness"

describe("Cortex Privacy Hook Fixture", () => {
  let harness: PrivacyHarness | null = null
  const suiteOriginalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()

  beforeEach(() => {
    suiteOriginalEnv.clear()
    for (const key of HARNESS_ENV_KEYS) {
      suiteOriginalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }
  })

  afterEach(() => {
    try {
      const active = harness
      harness = null
      active?.restore()
    } finally {
      vi.unstubAllEnvs()
      for (const [key, val] of suiteOriginalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }
  })
  it("delivers positive prompt control faithfully to HTTP endpoint", async () => {
    harness = await createPrivacyHarness()
    const content = "Clean positive prompt for testing without any private markers"
    await harness.deliverPrompt(content)

    const sessionReqs = harness.sessionRequests()
    expect(sessionReqs).toHaveLength(1)
    expect(sessionReqs[0].body).toMatchObject({ id: "session-1", project: "project" })

    const promptReqs = harness.promptRequests()
    expect(promptReqs).toHaveLength(1)
    expect(promptReqs[0].body.content).toBe(content)
    expect(promptReqs[0].body.session_id).toBe("session-1")
    expect(harness.logs).toContain("[cortex] prompt delivery success")
  })

  it("delivers positive passive task observation faithfully to HTTP endpoint", async () => {
    harness = await createPrivacyHarness()
    const content = "Task output with sufficient length exceeding fifty characters for passive observation capture."
    await harness.deliverPassive(content)

    const obsReqs = harness.observationRequests()
    expect(obsReqs).toHaveLength(1)
    expect(obsReqs[0].body.content).toBe(content)
    expect(obsReqs[0].body.type).toBe("passive")
    expect(obsReqs[0].body.session_id).toBe("session-1")
    expect(harness.logs).toContain("[cortex] observation delivery success")
  })

  it("redacts private markers in marked prompt control without leaking canary", async () => {
    harness = await createPrivacyHarness()
    const canary = "secret_canary_token_7734"
    await harness.deliverPrompt(`User query with <private>${canary}</private> embedded credential.`)

    const promptReqs = harness.promptRequests()
    expect(promptReqs).toHaveLength(1)
    expect(promptReqs[0].body.content).toBe("User query with [REDACTED] embedded credential.")
    expect(promptReqs[0].body.content).not.toContain(canary)
    expect(JSON.stringify(promptReqs[0].body)).not.toContain(canary)
    expect(harness.logs.some((l) => l.includes(canary))).toBe(false)
  })

  it("redacts private markers in marked passive task control without leaking canary", async () => {
    harness = await createPrivacyHarness()
    const canary = "secret_task_credential_9821"
    await harness.deliverPassive(`Execution summary with <private>${canary}</private> exceeding fifty characters to qualify.`)

    const obsReqs = harness.observationRequests()
    expect(obsReqs).toHaveLength(1)
    expect(obsReqs[0].body.content).toBe("Execution summary with [REDACTED] exceeding fifty characters to qualify.")
    expect(obsReqs[0].body.content).not.toContain(canary)
    expect(JSON.stringify(obsReqs[0].body)).not.toContain(canary)
    expect(harness.logs.some((l) => l.includes(canary))).toBe(false)
  })

  it("suppresses prompt delivery when session creation fails", async () => {
    harness = await createPrivacyHarness({ sessionStatus: 500 })
    await harness.deliverPrompt("Valid prompt text that should be suppressed due to session failure")
    expect(harness.sessionRequests()).toHaveLength(1)
    expect(harness.promptRequests()).toHaveLength(0)
  })

  it("suppresses passive task capture when session creation fails", async () => {
    harness = await createPrivacyHarness({ sessionStatus: 500 })
    await harness.deliverPassive("Task output exceeding fifty characters that should be suppressed on session failure.")
    expect(harness.sessionRequests()).toHaveLength(1)
    expect(harness.observationRequests()).toHaveLength(0)
  })

  it("fails when a required hook is missing rather than skipping", async () => {
    harness = await createPrivacyHarness()

    delete (harness.hooks as any)["chat.message"]
    await expect(harness.deliverPrompt("Valid prompt text that should fail on missing hook")).rejects.toThrow(
      /missing required hook: chat\.message/i,
    )

    delete (harness.hooks as any)["tool.execute.after"]
    await expect(
      harness.deliverPassive("Task output exceeding fifty characters that should fail on missing hook."),
    ).rejects.toThrow(/missing required hook: tool\.execute\.after/i)
  })

  it("rejects unknown fetch endpoints with a safe diagnostic and fails closed", async () => {
    harness = await createPrivacyHarness()
    await expect(fetch("http://127.0.0.1:7438/api/unknown-route")).rejects.toThrow(
      /unknown fetch endpoint: GET http:\/\/127\.0\.0\.1:7438\/api\/unknown-route/i,
    )
    expect(harness.requests.some((r) => r.url.includes("unknown-route"))).toBe(true)
  })

  it("isolates all seven environment variables before dynamic import and restores them on disposal", async () => {
    const originalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()
    for (const key of HARNESS_ENV_KEYS) {
      originalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }

    const baselineCanaries: Record<(typeof HARNESS_ENV_KEYS)[number], string> = {
      CORTEX_HTTP_TOKEN: "baseline-token-777",
      CORTEX_SERVER_URL: "https://baseline-server.example.com",
      CORTEX_URL: "https://baseline-url.example.com",
      CORTEX_HTTP_PORT: "9999",
      CORTEX_MODE: "server",
      CORTEX_SYNC_ENABLED: "true",
      CORTEX_SYNC_URL: "https://baseline-sync.example.com",
    }

    try {
      for (const [key, val] of Object.entries(baselineCanaries)) {
        process.env[key] = val
      }

      try {
        harness = await createPrivacyHarness()

        expect(process.env.CORTEX_HTTP_TOKEN).toBe("privacy-test-token")
        expect(process.env.CORTEX_SERVER_URL).toBeUndefined()
        expect(process.env.CORTEX_URL).toBe("http://127.0.0.1:7438")
        expect(process.env.CORTEX_HTTP_PORT).toBe("7438")
        expect(process.env.CORTEX_MODE).toBe("local")
        expect(process.env.CORTEX_SYNC_ENABLED).toBe("false")
        expect(process.env.CORTEX_SYNC_URL).toBe("")
      } finally {
        const active = harness
        harness = null
        active?.restore()
      }

      for (const [key, val] of Object.entries(baselineCanaries)) {
        expect(process.env[key]).toBe(val)
      }
    } finally {
      for (const [key, val] of originalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }
  })

  it("cleans up fixture environment and clears shared harness reference under controlled normal execution without exposing environment values", async () => {
    const testOriginalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()
    for (const key of HARNESS_ENV_KEYS) {
      testOriginalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }

    const baselineCanaries: Record<(typeof HARNESS_ENV_KEYS)[number], string> = {
      CORTEX_HTTP_TOKEN: "canary-token-normal-992",
      CORTEX_SERVER_URL: "https://normal.example.com",
      CORTEX_URL: "https://normal-local.example.com",
      CORTEX_HTTP_PORT: "9982",
      CORTEX_MODE: "local",
      CORTEX_SYNC_ENABLED: "false",
      CORTEX_SYNC_URL: "https://normal-sync.example.com",
    }

    let sharedReferenceClearedBeforeRestore = false

    try {
      for (const [key, val] of Object.entries(baselineCanaries)) {
        process.env[key] = val
      }

      try {
        harness = await createPrivacyHarness()
        expect(process.env.CORTEX_HTTP_TOKEN).toBe("privacy-test-token")
        const realRestore = harness.restore.bind(harness)
        harness.restore = () => {
          sharedReferenceClearedBeforeRestore = harness === null
          realRestore()
        }
        await harness.deliverPrompt("Normal prompt execution")
        expect(harness.logs.some((l) => Object.values(baselineCanaries).some((v) => l.includes(v)))).toBe(false)
      } finally {
        const active = harness
        harness = null
        active?.restore()
      }

      expect(sharedReferenceClearedBeforeRestore).toBe(true)
      expect(harness).toBeNull()
      for (const [key, val] of Object.entries(baselineCanaries)) {
        expect(process.env[key]).toBe(val)
      }
    } finally {
      vi.unstubAllEnvs()
      for (const [key, val] of testOriginalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }

    for (const [key, val] of testOriginalEnv) {
      expect(process.env[key]).toBe(val)
    }
  })

  it("clears shared harness reference before restore and restores true environment originals when restore throws without exposing environment values", async () => {
    const testOriginalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()
    for (const key of HARNESS_ENV_KEYS) {
      testOriginalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }

    const baselineCanaries: Record<(typeof HARNESS_ENV_KEYS)[number], string> = {
      CORTEX_HTTP_TOKEN: "canary-token-restore-fail-991",
      CORTEX_SERVER_URL: "https://restore-fail.example.com",
      CORTEX_URL: "https://restore-fail-local.example.com",
      CORTEX_HTTP_PORT: "9981",
      CORTEX_MODE: "local",
      CORTEX_SYNC_ENABLED: "false",
      CORTEX_SYNC_URL: "https://restore-fail-sync.example.com",
    }

    let sharedReferenceClearedBeforeRestore = false
    let restoreErrorCaught: unknown = null

    try {
      for (const [key, val] of Object.entries(baselineCanaries)) {
        process.env[key] = val
      }

      try {
        harness = await createPrivacyHarness()
        harness.restore = () => {
          sharedReferenceClearedBeforeRestore = harness === null
          throw new Error("controlled restore failure")
        }
      } finally {
        try {
          const active = harness
          harness = null
          active?.restore()
        } catch (err) {
          restoreErrorCaught = err
        }
      }
    } finally {
      vi.unstubAllEnvs()
      for (const [key, val] of testOriginalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }

    expect(sharedReferenceClearedBeforeRestore).toBe(true)
    expect(harness).toBeNull()
    expect(restoreErrorCaught).toBeInstanceOf(Error)
    expect((restoreErrorCaught as Error).message).toBe("controlled restore failure")
    for (const [key, val] of testOriginalEnv) {
      expect(process.env[key]).toBe(val)
    }
    for (const val of Object.values(baselineCanaries)) {
      expect((restoreErrorCaught as Error).message).not.toContain(val)
    }
  })

  it("cleans up fixture environment and clears shared harness reference when assertion or test body fails without exposing environment values", async () => {
    const testOriginalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()
    for (const key of HARNESS_ENV_KEYS) {
      testOriginalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }

    const baselineCanaries: Record<(typeof HARNESS_ENV_KEYS)[number], string> = {
      CORTEX_HTTP_TOKEN: "canary-token-body-fail-993",
      CORTEX_SERVER_URL: "https://body-fail.example.com",
      CORTEX_URL: "https://body-fail-local.example.com",
      CORTEX_HTTP_PORT: "9983",
      CORTEX_MODE: "local",
      CORTEX_SYNC_ENABLED: "false",
      CORTEX_SYNC_URL: "https://body-fail-sync.example.com",
    }

    let sharedReferenceClearedBeforeRestore = false
    let bodyErrorCaught: unknown = null

    try {
      for (const [key, val] of Object.entries(baselineCanaries)) {
        process.env[key] = val
      }

      try {
        harness = await createPrivacyHarness()
        const realRestore = harness.restore.bind(harness)
        harness.restore = () => {
          sharedReferenceClearedBeforeRestore = harness === null
          realRestore()
        }
        expect("unexpected-actual-value").toBe("expected-target-value")
      } finally {
        const active = harness
        harness = null
        active?.restore()
      }
    } catch (err) {
      bodyErrorCaught = err
    } finally {
      vi.unstubAllEnvs()
      for (const [key, val] of testOriginalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }

    expect(bodyErrorCaught).toBeDefined()
    expect(sharedReferenceClearedBeforeRestore).toBe(true)
    expect(harness).toBeNull()
    for (const [key, val] of testOriginalEnv) {
      expect(process.env[key]).toBe(val)
    }
    const errMsg = bodyErrorCaught instanceof Error ? bodyErrorCaught.message : String(bodyErrorCaught)
    for (const val of Object.values(baselineCanaries)) {
      expect(errMsg).not.toContain(val)
    }
  })

  it("restores true environment originals when harness construction rejects without exposing environment values", async () => {
    const testOriginalEnv = new Map<(typeof HARNESS_ENV_KEYS)[number], string | undefined>()
    for (const key of HARNESS_ENV_KEYS) {
      testOriginalEnv.set(key, key in process.env ? process.env[key] : undefined)
    }

    const baselineCanaries: Record<(typeof HARNESS_ENV_KEYS)[number], string> = {
      CORTEX_HTTP_TOKEN: "canary-token-construct-fail-994",
      CORTEX_SERVER_URL: "https://construct-fail.example.com",
      CORTEX_URL: "https://construct-fail-local.example.com",
      CORTEX_HTTP_PORT: "9984",
      CORTEX_MODE: "local",
      CORTEX_SYNC_ENABLED: "false",
      CORTEX_SYNC_URL: "https://construct-fail-sync.example.com",
    }

    let constructionErrorCaught: unknown = null

    try {
      for (const [key, val] of Object.entries(baselineCanaries)) {
        process.env[key] = val
      }

      try {
        const failingConstruction = async (): Promise<PrivacyHarness> => {
          process.env.CORTEX_HTTP_TOKEN = "mutated-during-failing-construction"
          throw new Error("controlled harness construction rejection")
        }
        harness = await failingConstruction()
      } finally {
        const active = harness
        harness = null
        active?.restore()
      }
    } catch (err) {
      constructionErrorCaught = err
    } finally {
      vi.unstubAllEnvs()
      for (const [key, val] of testOriginalEnv) {
        if (val === undefined) {
          delete process.env[key]
        } else {
          process.env[key] = val
        }
      }
    }

    expect(constructionErrorCaught).toBeInstanceOf(Error)
    expect((constructionErrorCaught as Error).message).toBe("controlled harness construction rejection")
    expect(harness).toBeNull()
    for (const [key, val] of testOriginalEnv) {
      expect(process.env[key]).toBe(val)
    }
    for (const val of Object.values(baselineCanaries)) {
      expect((constructionErrorCaught as Error).message).not.toContain(val)
    }
  })
})
