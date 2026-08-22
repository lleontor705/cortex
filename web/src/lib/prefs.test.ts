import { describe, expect, it } from "vitest";
import {
  LEGACY_SECRET_KEYS,
  loadPreferences,
  purgeLegacySecrets,
  saveLLMPreferences,
  saveServerUrl,
} from "./prefs";

class FakeStorage {
  private map = new Map<string, string>();

  getItem(key: string): string | null {
    return this.map.has(key) ? (this.map.get(key) as string) : null;
  }

  setItem(key: string, value: string): void {
    this.map.set(key, String(value));
  }

  removeItem(key: string): void {
    this.map.delete(key);
  }

  keys(): string[] {
    return [...this.map.keys()];
  }
}

function seededStorage(): FakeStorage {
  const storage = new FakeStorage();
  storage.setItem("cortex_server_url", "https://cortex.example");
  storage.setItem("cortex_llm_provider", "anthropic");
  storage.setItem("cortex_llm_model", "claude-3");
  storage.setItem("cortex_llm_base_url", "https://api.anthropic.com/v1");
  // Secrets persisted by legacy versions of the web client.
  storage.setItem("cortex_token", "secret-bearer-value");
  storage.setItem("cortex_llm_key", "sk-secret-llm-key");
  return storage;
}

describe("web preference storage (secret-free by construction)", () => {
  it("returns defaults for empty storage", () => {
    const prefs = loadPreferences(new FakeStorage());
    expect(prefs).toEqual({
      serverUrl: "http://localhost:7438",
      llmProvider: "gemini",
      llmModel: "gemini-2.5-flash",
      llmBaseURL: "",
      embeddingProvider: "gemini",
      embeddingModel: "text-embedding-004",
      embeddingDimensions: 768,
      vectorProvider: "pgvector",
    });
  });

  it("loads only the allowlisted non-secret preference keys", () => {
    const prefs = loadPreferences(seededStorage());
    expect(prefs).toEqual({
      serverUrl: "https://cortex.example",
      llmProvider: "anthropic",
      llmModel: "claude-3",
      llmBaseURL: "https://api.anthropic.com/v1",
      embeddingProvider: "gemini",
      embeddingModel: "text-embedding-004",
      embeddingDimensions: 768,
      vectorProvider: "pgvector",
    });
  });

  it("saves only non-secret preferences", () => {
    const storage = new FakeStorage();
    saveServerUrl(storage, "https://cortex.example");
    saveLLMPreferences(storage, "openrouter", "some-model", "https://openrouter.ai/api/v1");

    expect(storage.keys().sort()).toEqual([
      "cortex_llm_base_url",
      "cortex_llm_model",
      "cortex_llm_provider",
      "cortex_server_url",
    ]);
  });

  it("purges legacy persisted secrets and keeps non-secret preferences", () => {
    const storage = seededStorage();
    const removed = purgeLegacySecrets(storage);

    expect(removed.sort()).toEqual([...LEGACY_SECRET_KEYS].sort());
    expect(storage.getItem("cortex_token")).toBeNull();
    expect(storage.getItem("cortex_llm_key")).toBeNull();
    expect(storage.getItem("cortex_server_url")).toBe("https://cortex.example");

    // Storage canary: after load/save/purge, no secret key may survive.
    expect(storage.keys()).not.toEqual(
      expect.arrayContaining([...LEGACY_SECRET_KEYS]),
    );
  });

  it("purge is idempotent and reports nothing on clean storage", () => {
    const storage = new FakeStorage();
    expect(purgeLegacySecrets(storage)).toEqual([]);
    expect(purgeLegacySecrets(storage)).toEqual([]);
  });

  it("the loaded preferences shape has no field for any secret", () => {
    const prefs = loadPreferences(seededStorage());
    expect(Object.keys(prefs).sort()).toEqual([
      "embeddingDimensions",
      "embeddingModel",
      "embeddingProvider",
      "llmBaseURL",
      "llmModel",
      "llmProvider",
      "serverUrl",
      "vectorProvider",
    ]);
  });
});
