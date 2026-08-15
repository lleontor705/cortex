"use client";

import React, { createContext, useContext, useState, useEffect } from "react";
import { CortexClient, Principal, ServerStats } from "./api";

interface AuthContextType {
  serverUrl: string;
  token: string;
  llmApiKey: string;
  llmProvider: string;
  llmModel: string;
  client: CortexClient | null;
  principal: Principal | null;
  stats: ServerStats | null;
  isConnected: boolean;
  isLoading: boolean;
  error: string | null;
  setCredentials: (url: string, token: string) => Promise<boolean>;
  setLLMCredentials: (apiKey: string, provider: string, model: string) => void;
  refreshState: () => Promise<void>;
  logout: () => void;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [serverUrl, setServerUrl] = useState<string>("http://localhost:7438");
  const [token, setToken] = useState<string>("");
  const [llmApiKey, setLlmApiKey] = useState<string>("");
  const [llmProvider, setLlmProvider] = useState<string>("openai");
  const [llmModel, setLlmModel] = useState<string>("gpt-4o-mini");

  const [client, setClient] = useState<CortexClient | null>(null);
  const [principal, setPrincipal] = useState<Principal | null>(null);
  const [stats, setStats] = useState<ServerStats | null>(null);
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Load persisted credentials on mount
    const savedUrl = localStorage.getItem("cortex_server_url") || "http://localhost:7438";
    const savedToken = localStorage.getItem("cortex_token") || "";
    const savedLLMKey = localStorage.getItem("cortex_llm_key") || "";
    const savedLLMProvider = localStorage.getItem("cortex_llm_provider") || "openai";
    const savedLLMModel = localStorage.getItem("cortex_llm_model") || "gpt-4o-mini";

    setServerUrl(savedUrl);
    setToken(savedToken);
    setLlmApiKey(savedLLMKey);
    setLlmProvider(savedLLMProvider);
    setLlmModel(savedLLMModel);

    if (savedToken) {
      const cli = new CortexClient(savedUrl, savedToken, () => {
        setIsConnected(false);
        setError("Session expired or unauthorized");
      });
      setClient(cli);

      cli.health()
        .then(() => cli.me())
        .then((p) => {
          setPrincipal(p);
          setIsConnected(true);
          return cli.stats();
        })
        .then((s) => {
          setStats(s);
          setError(null);
        })
        .catch((err) => {
          setIsConnected(false);
          setError(err.message || "Failed to connect to Cortex server");
        })
        .finally(() => {
          setIsLoading(false);
        });
    } else {
      setIsLoading(false);
    }
  }, []);

  const setCredentials = async (url: string, tok: string): Promise<boolean> => {
    setIsLoading(true);
    setError(null);
    try {
      const cli = new CortexClient(url, tok, () => {
        setIsConnected(false);
        setError("Unauthorized");
      });

      await cli.health();
      const p = await cli.me().catch(() => null);
      const s = await cli.stats().catch(() => null);

      setServerUrl(url);
      setToken(tok);
      setClient(cli);
      setPrincipal(p);
      setStats(s);
      setIsConnected(true);

      localStorage.setItem("cortex_server_url", url);
      localStorage.setItem("cortex_token", tok);
      return true;
    } catch (err: any) {
      setError(err.message || "Failed to authenticate with Cortex");
      setIsConnected(false);
      return false;
    } finally {
      setIsLoading(false);
    }
  };

  const setLLMCredentials = (apiKey: string, provider: string, model: string) => {
    setLlmApiKey(apiKey);
    setLlmProvider(provider);
    setLlmModel(model);
    localStorage.setItem("cortex_llm_key", apiKey);
    localStorage.setItem("cortex_llm_provider", provider);
    localStorage.setItem("cortex_llm_model", model);
  };

  const refreshState = async () => {
    if (!client) return;
    try {
      const [p, s] = await Promise.all([
        client.me().catch(() => null),
        client.stats().catch(() => null),
      ]);
      setPrincipal(p);
      setStats(s);
    } catch (err) {
      console.error("Failed to refresh state", err);
    }
  };

  const logout = () => {
    setToken("");
    setClient(null);
    setPrincipal(null);
    setStats(null);
    setIsConnected(false);
    localStorage.removeItem("cortex_token");
  };

  return (
    <AuthContext.Provider
      value={{
        serverUrl,
        token,
        llmApiKey,
        llmProvider,
        llmModel,
        client,
        principal,
        stats,
        isConnected,
        isLoading,
        error,
        setCredentials,
        setLLMCredentials,
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
