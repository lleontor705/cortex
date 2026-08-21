// Secret-free web preference storage.
//
// Bearer tokens and LLM API keys live ONLY in React state (memory of the
// live tab). This module is the single surface allowed to touch
// localStorage, and it persists exclusively non-secret preference keys.
// Legacy secret keys persisted by older builds are purged on startup.

export const SERVER_URL_KEY = "cortex_server_url";
export const LLM_PROVIDER_KEY = "cortex_llm_provider";
export const LLM_MODEL_KEY = "cortex_llm_model";
export const LLM_BASE_URL_KEY = "cortex_llm_base_url";

/** Secret keys written by legacy builds; removed eagerly on startup. */
export const LEGACY_SECRET_KEYS = ["cortex_token", "cortex_llm_key"] as const;

export interface WebPreferences {
  serverUrl: string;
  llmProvider: string;
  llmModel: string;
  llmBaseURL: string;
}

export function loadPreferences(storage: Pick<Storage, "getItem">): WebPreferences {
  return {
    serverUrl: storage.getItem(SERVER_URL_KEY) || "http://localhost:7438",
    llmProvider: storage.getItem(LLM_PROVIDER_KEY) || "openai",
    llmModel: storage.getItem(LLM_MODEL_KEY) || "gpt-4o-mini",
    llmBaseURL: storage.getItem(LLM_BASE_URL_KEY) || "",
  };
}

export function saveServerUrl(storage: Pick<Storage, "setItem">, url: string): void {
  storage.setItem(SERVER_URL_KEY, url);
}

export function saveLLMPreferences(
  storage: Pick<Storage, "setItem">,
  provider: string,
  model: string,
  baseURL: string = "",
): void {
  storage.setItem(LLM_PROVIDER_KEY, provider);
  storage.setItem(LLM_MODEL_KEY, model);
  storage.setItem(LLM_BASE_URL_KEY, baseURL);
}

/** Removes secrets persisted by legacy builds; returns the purged keys. */
export function purgeLegacySecrets(
  storage: Pick<Storage, "getItem" | "removeItem">,
): string[] {
  const removed: string[] = [];
  for (const key of LEGACY_SECRET_KEYS) {
    if (storage.getItem(key) !== null) {
      storage.removeItem(key);
      removed.push(key);
    }
  }
  return removed;
}
