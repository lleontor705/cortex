"use client";

import React, { createContext, useContext, useRef, useState, useEffect } from "react";
import { CortexClient, Principal, ServerStats } from "./api";
import { AuthAttemptCoordinator } from "./auth-attempts";
import {
  loadPreferences,
  purgeLegacySecrets,
  saveEmbeddingPreferences,
  saveLLMPreferences,
  saveServerUrl,
} from "./prefs";
import { refreshSnapshot, runLoginHandshake } from "./auth-handshake";
import { validateBearerDestination } from "./transport-policy";

interface AuthContextType {
  serverUrl: string;
  token: string;
  /**
   * Reset generation: advances on every terminal auth event (initial-login
   * 401, logout, 401 invalidation, or a superseded/abandoned attempt).
   * Secret-holding components key their local secret reset on this number
   * instead of on the token VALUE, because a value-keyed mirror cannot
   * clear typed state when the provider token was already empty.
   */
  resetGeneration: number;
  llmApiKey: string;
  llmProvider: string;
  llmModel: string;
  llmBaseURL: string;
  embeddingProvider: string;
  embeddingModel: string;
  embeddingDimensions: number;
  vectorProvider: string;
  client: CortexClient | null;
  principal: Principal | null;
  stats: ServerStats | null;
  isConnected: boolean;
  isLoading: boolean;
  error: string | null;
  setCredentials: (url: string, token: string) => Promise<boolean>;
  setLLMCredentials: (apiKey: string, provider: string, model: string, baseURL?: string) => void;
  setEmbeddingCredentials: (provider: string, model: string, dimensions: number, vectorProvider?: string) => void;
  refreshState: () => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [serverUrl, setServerUrl] = useState<string>("http://localhost:7438");
  const [token, setToken] = useState<string>("");
  const [llmApiKey, setLlmApiKey] = useState<string>("");
  const [llmProvider, setLlmProvider] = useState<string>("gemini");
  const [llmModel, setLlmModel] = useState<string>("gemini-2.5-flash");
  const [llmBaseURL, setLlmBaseURL] = useState<string>("");
  const [embeddingProvider, setEmbeddingProvider] = useState<string>("gemini");
  const [embeddingModel, setEmbeddingModel] = useState<string>("text-embedding-004");
  const [embeddingDimensions, setEmbeddingDimensions] = useState<number>(768);
  const [vectorProvider, setVectorProvider] = useState<string>("pgvector");

  const [client, setClient] = useState<CortexClient | null>(null);
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [stats, setStats] = useState<ServerStats | null>(null);
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);
  const [resetGeneration, setResetGeneration] = useState<number>(0);

  // Serializes concurrent logins and makes logout/401 terminal: a handshake
  // may only commit while it still owns the coordinator's session slot.
  // Every terminal supersede advances the coordinator's epoch and pushes it
  // into resetGeneration, the explicit reset EVENT consumed by every
  // secret-holding component.
  const attemptsRef = useRef<AuthAttemptCoordinator | null>(null);
  if (!attemptsRef.current) {
    attemptsRef.current = new AuthAttemptCoordinator((generation) => {
      setResetGeneration(generation);
    });
  }

  useEffect(() => {
    // Bearer tokens and LLM keys are memory-only: they are never read from
    // (or written to) any persistent storage, so a reload always starts
    // disconnected. Only non-secret preferences survive restarts, and any
    // secrets persisted by legacy builds are purged eagerly.
    const storage = window.localStorage;
    purgeLegacySecrets(storage);
    const prefs = loadPreferences(storage);
    setServerUrl(prefs.serverUrl);
    setLlmProvider(prefs.llmProvider);
    setLlmModel(prefs.llmModel);
    setLlmBaseURL(prefs.llmBaseURL);
    setEmbeddingProvider(prefs.embeddingProvider);
    setEmbeddingModel(prefs.embeddingModel);
    setEmbeddingDimensions(prefs.embeddingDimensions);
    setVectorProvider(prefs.vectorProvider);
    setIsLoading(false);
  }, []);

  // Single sweeper for every live secret reference in this provider.
  // Used by both logout and the 401 invalidation callback so no path can
  // leave a bearer token, LLM key, client, or snapshot behind. It also
  // supersedes the attempt coordinator, so any still-pending handshake
  // becomes terminal and its client is aborted before state is cleared.
  const clearLiveSecrets = () => {
    attemptsRef.current?.supersede();
    setToken("");
    setLlmApiKey("");
    setClient(null);
    setPrincipal(null);
    setStats(null);
    setIsConnected(false);
    setIsLoading(false);
  };

  const setCredentials = async (url: string, tok: string): Promise<boolean> => {
    setIsLoading(true);
    setError(null);

    // Enforce HTTPS (or strict-loopback HTTP) before any client exists
    // that could transmit the bearer token.
    try {
      validateBearerDestination(url);
    } catch (err: any) {
      setError(err.message || "Failed to authenticate with Cortex");
      setIsConnected(false);
      setIsLoading(false);
      return false;
    }

    const coordinator = attemptsRef.current!;
    // Set when the client invalidates itself (401) during the handshake.
    let invalidated = false;
    let epoch = -1;
    const cli = new CortexClient(url, tok, () => {
      // The client already invalidated itself (token cleared, in-flight
      // requests aborted) before invoking this callback. The sweep is only
      // allowed while this attempt still owns the session slot: a stale
      // attempt's late 401 must not clear a newer session's state.
      invalidated = true;
      if (epoch >= 0 && coordinator.isCurrent(epoch)) {
        clearLiveSecrets();
        setError("Session expired or unauthorized");
      }
    });
    // Supersedes (and aborts) any earlier pending attempt before this one
    // becomes the slot owner.
    epoch = coordinator.begin(cli);

    try {
      const result = await runLoginHandshake(cli);
      if (!coordinator.finish(epoch, cli)) {
        // A logout, 401 invalidation, or newer attempt superseded this
        // handshake while it was in flight: terminal, commit nothing.
        return false;
      }
      if (!result.ok || invalidated) {
        // A 401 anywhere in the handshake is terminal: the client has
        // already cleared its token, so the React token/connected state
        // must never be restored from this attempt.
        coordinator.abandon(epoch, cli);
        setIsConnected(false);
        setError(
          result.ok
            ? "Session expired or unauthorized"
            : result.message || "Failed to authenticate with Cortex",
        );
        return false;
      }

      setServerUrl(url);
      setToken(tok);
      setClient(cli);
      setPrincipal(result.principal);
      setStats(result.stats);
      setIsConnected(true);

      saveServerUrl(window.localStorage, url);
      return true;
    } finally {
      setIsLoading(false);
    }
  };

  const setLLMCredentials = (
    apiKey: string,
    provider: string,
    model: string,
    baseURL: string = "",
  ) => {
    setLlmApiKey(apiKey);
    setLlmProvider(provider);
    setLlmModel(model);
    setLlmBaseURL(baseURL);
    saveLLMPreferences(window.localStorage, provider, model, baseURL);
  };

  const setEmbeddingCredentials = (
    provider: string,
    model: string,
    dimensions: number,
    vecProvider: string = "pgvector",
  ) => {
    setEmbeddingProvider(provider);
    setEmbeddingModel(model);
    setEmbeddingDimensions(dimensions);
    setVectorProvider(vecProvider);
    saveEmbeddingPreferences(window.localStorage, provider, model, dimensions, vecProvider);
  };

  const refreshState = async () => {
    if (!client) return;
    const snapshot = await refreshSnapshot(client);
    if (snapshot.expired) {
      return;
    }
    if (!attemptsRef.current?.owns(client)) {
      return;
    }
    setPrincipal(snapshot.principal);
    setStats(snapshot.stats);
  };

  const logout = () => {
    client?.invalidate();
    clearLiveSecrets();
  };

  return (
    <AuthContext.Provider
      value={{
        serverUrl,
        token,
        resetGeneration,
        llmApiKey,
        llmProvider,
        llmModel,
        llmBaseURL,
        embeddingProvider,
        embeddingModel,
        embeddingDimensions,
        vectorProvider,
        client,
        principal,
        stats,
        isConnected,
        isLoading,
        error,
        setCredentials,
        setLLMCredentials,
        setEmbeddingCredentials,
        refreshState,
        logout,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return context;
}
