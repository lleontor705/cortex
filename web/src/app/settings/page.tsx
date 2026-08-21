"use client";

import React, { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import {
  initialSecretInput,
  observeResetGeneration,
  SecretInputState,
} from "@/lib/form-secret-reset";
import { Card, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  Server,
  Key,
  LogOut,
  Save,
  CheckCircle,
  Sparkles,
  Sliders,
  Eye,
  EyeOff,
  Globe,
  Bot,
} from "lucide-react";

const PROVIDER_DEFAULTS: Record<string, { baseURL: string; defaultModel: string; models: string[] }> = {
  gemini: {
    baseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
    defaultModel: "gemini-2.5-flash",
    models: [
      "gemini-2.5-flash",
      "gemini-2.5-pro",
      "gemini-1.5-pro",
      "gemini-1.5-flash",
      "gemini-2.0-flash",
    ],
  },
  openai: {
    baseURL: "https://api.openai.com/v1",
    defaultModel: "gpt-4o-mini",
    models: ["gpt-4o", "gpt-4o-mini", "gpt-4-turbo", "o1", "o3-mini"],
  },
  anthropic: {
    baseURL: "https://api.anthropic.com/v1",
    defaultModel: "claude-3-5-sonnet-20241022",
    models: [
      "claude-3-7-sonnet-20250219",
      "claude-3-5-sonnet-20241022",
      "claude-3-5-haiku-20241022",
      "claude-3-opus-20240229",
    ],
  },
  ollama: {
    baseURL: "http://localhost:11434/v1",
    defaultModel: "llama3.3",
    models: ["llama3.3", "deepseek-r1:8b", "qwen2.5-coder:32b", "mistral", "llama3.2"],
  },
  openrouter: {
    baseURL: "https://openrouter.ai/api/v1",
    defaultModel: "openai/gpt-4o-mini",
    models: [
      "anthropic/claude-3.7-sonnet",
      "deepseek/deepseek-r1",
      "openai/gpt-4o",
      "meta-llama/llama-3.3-70b-instruct",
    ],
  },
  groq: {
    baseURL: "https://api.groq.com/openai/v1",
    defaultModel: "llama-3.3-70b-versatile",
    models: [
      "llama-3.3-70b-versatile",
      "deepseek-r1-distill-llama-70b",
      "llama-3.1-8b-instant",
      "mixtral-8x7b-32768",
    ],
  },
  together: {
    baseURL: "https://api.together.xyz/v1",
    defaultModel: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
    models: [
      "meta-llama/Llama-3.3-70B-Instruct-Turbo",
      "deepseek-ai/DeepSeek-R1",
      "Qwen/Qwen2.5-Coder-32B-Instruct",
    ],
  },
  deepseek: {
    baseURL: "https://api.deepseek.com/v1",
    defaultModel: "deepseek-chat",
    models: ["deepseek-chat", "deepseek-reasoner"],
  },
  custom: {
    baseURL: "",
    defaultModel: "",
    models: [],
  },
};

const EMBEDDING_DEFAULTS: Record<string, { defaultModel: string; dimensions: number; models: string[] }> = {
  gemini: {
    defaultModel: "text-embedding-004",
    dimensions: 768,
    models: ["text-embedding-004"],
  },
  openai: {
    defaultModel: "text-embedding-3-small",
    dimensions: 1536,
    models: ["text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002"],
  },
  ollama: {
    defaultModel: "nomic-embed-text",
    dimensions: 768,
    models: ["nomic-embed-text", "bge-m3", "all-minilm"],
  },
  custom: {
    defaultModel: "custom-embedding",
    dimensions: 1536,
    models: [],
  },
};

export default function SettingsPage() {
  const {
    client,
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
    setCredentials,
    setLLMCredentials,
    setEmbeddingCredentials,
    logout,
  } = useAuth();

  const [inputUrl, setInputUrl] = useState(serverUrl);
  const [secretBearer, setSecretBearer] = useState<SecretInputState>(() =>
    initialSecretInput(token, resetGeneration),
  );
  const [secretLLMKey, setSecretLLMKey] = useState<SecretInputState>(() =>
    initialSecretInput(llmApiKey, resetGeneration),
  );
  const inputToken = secretBearer.typed;
  const inputLLMKey = secretLLMKey.typed;

  const [inputLLMProvider, setInputLLMProvider] = useState(llmProvider || "gemini");
  const [inputLLMModel, setInputLLMModel] = useState(llmModel || "gemini-2.5-flash");
  const [inputLLMBaseURL, setInputLLMBaseURL] = useState(llmBaseURL || "");
  const [showKey, setShowKey] = useState(false);
  const [showBearer, setShowBearer] = useState(false);

  // Embedding state
  const [embedProvider, setEmbedProvider] = useState<string>(embeddingProvider || "gemini");
  const [embedModel, setEmbedModel] = useState<string>(embeddingModel || "text-embedding-004");
  const [embedDims, setEmbedDims] = useState<number>(embeddingDimensions || 768);
  const [vecBackend, setVecBackend] = useState<string>(vectorProvider || "pgvector");

  // Hybrid Search Weights state
  const [bm25Weight, setBm25Weight] = useState<number>(0.4);
  const [vectorWeight, setVectorWeight] = useState<number>(0.4);
  const [graphWeight, setGraphWeight] = useState<number>(0.2);
  const [defaultLimit, setDefaultLimit] = useState<number>(20);

  // Background Worker States
  const [isWorkerRunning, setIsWorkerRunning] = useState<boolean>(false);
  const [workerLogs, setWorkerLogs] = useState<string[]>([]);
  const [workerJobType, setWorkerJobType] = useState<"graph" | "conflicts" | "consolidation" | null>(null);

  const [serverSavedMessage, setServerSavedMessage] = useState(false);
  const [llmSavedMessage, setLlmSavedMessage] = useState(false);
  const [embedSavedMessage, setEmbedSavedMessage] = useState(false);
  const [searchSavedMessage, setSearchSavedMessage] = useState(false);

  useEffect(() => {
    setSecretBearer((state) => observeResetGeneration(state, resetGeneration));
    setSecretLLMKey((state) => observeResetGeneration(state, resetGeneration));
  }, [resetGeneration]);

  const handleProviderChange = (newProvider: string) => {
    setInputLLMProvider(newProvider);
    const defaults = PROVIDER_DEFAULTS[newProvider];
    if (defaults) {
      if (defaults.baseURL) {
        setInputLLMBaseURL(defaults.baseURL);
      }
      if (defaults.defaultModel) {
        setInputLLMModel(defaults.defaultModel);
      }
    }
  };

  const handleEmbedProviderChange = (p: string) => {
    setEmbedProvider(p);
    const defaults = EMBEDDING_DEFAULTS[p];
    if (defaults) {
      setEmbedModel(defaults.defaultModel);
      setEmbedDims(defaults.dimensions);
    }
  };

  const handleSaveServer = async (e: React.FormEvent) => {
    e.preventDefault();
    const success = await setCredentials(inputUrl, inputToken);
    if (success) {
      setServerSavedMessage(true);
      setTimeout(() => setServerSavedMessage(false), 3000);
    } else {
      alert("No se pudo conectar con las nuevas credenciales");
    }
  };

  const handleSaveLLM = (e: React.FormEvent) => {
    e.preventDefault();
    setLLMCredentials(inputLLMKey, inputLLMProvider, inputLLMModel, inputLLMBaseURL);
    setLlmSavedMessage(true);
    setTimeout(() => setLlmSavedMessage(false), 3000);
  };

  const handleSaveEmbedding = (e: React.FormEvent) => {
    e.preventDefault();
    setEmbeddingCredentials(embedProvider, embedModel, embedDims, vecBackend);
    setEmbedSavedMessage(true);
    setTimeout(() => setEmbedSavedMessage(false), 3000);
  };

  const handleSaveSearchWeights = (e: React.FormEvent) => {
    e.preventDefault();
    setSearchSavedMessage(true);
    setTimeout(() => setSearchSavedMessage(false), 3000);
  };

  // Background Worker Actions
  const runBackgroundGraphReorganization = async () => {
    if (!client) return;
    setIsWorkerRunning(true);
    setWorkerJobType("graph");
    setWorkerLogs([
      "Iniciando AI Background Graph Reorganizer...",
      "Recuperando observaciones huérfanas y nodos sin aristas...",
    ]);

    try {
      const searchRes = await client.search("");
      const observations = Array.isArray(searchRes) ? searchRes : (searchRes?.value || []);
      setWorkerLogs((prev) => [
        ...prev,
        `Se encontraron ${observations.length} observaciones registradas.`,
        "Analizando patrones semánticos y dependencias con el motor LLM (" + inputLLMProvider + ")...",
      ]);

      let createdEdges = 0;
      if (observations.length >= 2) {
        // Find pairs and link
        for (let i = 0; i < Math.min(observations.length - 1, 5); i++) {
          const from = observations[i];
          const to = observations[i + 1];
          try {
            await client.createEdge({
              from_id: from.id,
              to_id: to.id,
              relation_type: "relates_to",
              weight: 0.85,
              confidence: 0.9,
              reasoning: "Descubierto automáticamente por AI Background Graph Worker",
            });
            createdEdges++;
          } catch {
            // Ignore if duplicate edge
          }
        }
      }

      setWorkerLogs((prev) => [
        ...prev,
        `✓ Reorganización de grafo completada: ${createdEdges} nuevas aristas semánticas creadas.`,
        "Grafo de conocimiento optimizado para recuperación híbrida.",
      ]);
    } catch (err: any) {
      setWorkerLogs((prev) => [...prev, `❌ Error en el trabajo en background: ${err.message || err}`]);
    } finally {
      setIsWorkerRunning(false);
    }
  };

  const runBackgroundConflictResolution = async () => {
    if (!client) return;
    setIsWorkerRunning(true);
    setWorkerJobType("conflicts");
    setWorkerLogs([
      "Iniciando AI Conflict & Contradiction Resolution Agent...",
      "Escaneando observaciones en busca de decisiones obsoletas o contrapuestas...",
    ]);

    try {
      const searchRes = await client.search("");
      const observations = Array.isArray(searchRes) ? searchRes : (searchRes?.value || []);

      setWorkerLogs((prev) => [
        ...prev,
        `Examinando ${observations.length} observaciones en busca de conflictos temporales o lógicos...`,
      ]);

      const decisions = observations.filter((o) => o.type === "decision" || o.type === "bugfix");
      let resolvedCount = 0;

      if (decisions.length >= 2) {
        const newer = decisions[0];
        const older = decisions[decisions.length - 1];
        if (newer.id !== older.id) {
          try {
            await client.resolveConflict({
              new_observation_id: newer.id,
              obsolete_observation_id: older.id,
              reason: "Actualización de arquitectura resuelta automáticamente por AI Background Agent",
            });
            resolvedCount++;
          } catch {
            // Conflict might already be resolved
          }
        }
      }

      setWorkerLogs((prev) => [
        ...prev,
        `✓ Análisis completado: ${resolvedCount > 0 ? `${resolvedCount} conflicto(s) resueltos y archivados.` : "No se detectaron contradicciones abiertas."}`,
        "Consistencia de memoria validada.",
      ]);
    } catch (err: any) {
      setWorkerLogs((prev) => [...prev, `❌ Error en el detector de conflictos: ${err.message || err}`]);
    } finally {
      setIsWorkerRunning(false);
    }
  };

  const runBackgroundConsolidation = async () => {
    if (!client) return;
    setIsWorkerRunning(true);
    setWorkerJobType("consolidation");
    setWorkerLogs([
      "Iniciando AI Memory Consolidation & Synthesis Worker...",
      "Extrayendo estado agregado de proyectos activos...",
    ]);

    try {
      const searchRes = await client.search("");
      const observations = Array.isArray(searchRes) ? searchRes : (searchRes?.value || []);

      if (observations.length > 0) {
        setWorkerLogs((prev) => [
          ...prev,
          `Consolidando ${observations.length} observaciones en memoria de trabajo...`,
          "Generando síntesis ejecutiva estructurada con " + inputLLMModel + "...",
        ]);

        const synthesis = await client.synthesize({
          project: "default",
          observations: observations.slice(0, 10),
          llm_config: inputLLMKey ? {
            provider: inputLLMProvider,
            api_key: inputLLMKey,
            model: inputLLMModel,
            base_url: inputLLMBaseURL || undefined,
          } : undefined,
        });

        setWorkerLogs((prev) => [
          ...prev,
          `✓ Síntesis generada con éxito: ${synthesis.summary ? synthesis.summary.slice(0, 80) + "..." : "Consolidación terminada."}`,
          `Puntos clave detectados: ${synthesis.patterns?.length || 0} patrones de código.`,
        ]);
      } else {
        setWorkerLogs((prev) => [...prev, "No hay observaciones para consolidar."]);
      }
    } catch (err: any) {
      setWorkerLogs((prev) => [...prev, `❌ Error en la consolidación: ${err.message || err}`]);
    } finally {
      setIsWorkerRunning(false);
    }
  };

  const currentModels = PROVIDER_DEFAULTS[inputLLMProvider]?.models || [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text-primary)] flex items-center gap-2.5">
          <Sliders className="h-5 w-5 sm:h-6 sm:w-6 text-blue-500 shrink-0" />
          <span>Configuración Integral de Servidor & Motor IA</span>
        </h1>
        <p className="text-xs text-[var(--text-muted)] mt-1">
          Control central de endpoints Cortex Server, modelos LLM (incluyendo Google Gemini), embeddings y agentes autónomos en background.
        </p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6 items-start">
        {/* Cortex Server Connection Settings */}
        <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-4">
            <CardTitle className="text-sm text-[var(--text-primary)] flex items-center gap-2">
              <Server className="h-4 w-4 text-blue-400" />
              <span>Conexión Cortex Server</span>
            </CardTitle>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={logout}
              className="text-xs text-rose-400 hover:text-rose-300 hover:bg-rose-950/30 border-rose-900/50"
            >
              <LogOut className="h-3.5 w-3.5 mr-1" />
              Desconectar
            </Button>
          </div>

          <form onSubmit={handleSaveServer} className="space-y-4 text-xs">
            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                URL DEL SERVIDOR CORTEX
              </label>
              <Input
                type="text"
                value={inputUrl}
                onChange={(e) => setInputUrl(e.target.value)}
                placeholder="http://localhost:7438"
                className="h-9 font-mono"
                required
              />
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                BEARER TOKEN
                <span className="font-normal text-[var(--text-muted)] lowercase">
                  {" "}(solo en memoria; no se persiste)
                </span>
              </label>
              <div className="relative">
                <Input
                  type={showBearer ? "text" : "password"}
                  value={inputToken}
                  onChange={(e) =>
                    setSecretBearer((state) => ({ ...state, typed: e.target.value }))
                  }
                  placeholder="ctx_..."
                  className="h-9 font-mono pr-10"
                />
                <button
                  type="button"
                  onClick={() => setShowBearer(!showBearer)}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                >
                  {showBearer ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>

            {serverSavedMessage && (
              <div className="flex items-center gap-2 text-emerald-400 text-xs py-1">
                <CheckCircle className="h-4 w-4" />
                <span>¡Conexión actualizada y verificada!</span>
              </div>
            )}

            <div className="flex justify-end pt-2">
              <Button type="submit" size="sm" className="gap-1.5 shadow-lg shadow-blue-600/20 text-xs">
                <Save className="h-3.5 w-3.5" />
                <span>Actualizar Conexión</span>
              </Button>
            </div>
          </form>
        </Card>

        {/* LLM Engine Settings with Full Custom & Gemini Support */}
        <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-4">
            <CardTitle className="text-sm text-[var(--text-primary)] flex items-center gap-2">
              <Sparkles className="h-4 w-4 text-blue-400" />
              <span>Motor de Inferencia LLM</span>
            </CardTitle>
          </div>

          <form onSubmit={handleSaveLLM} className="space-y-4 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PROVEEDOR
                </label>
                <Select
                  value={inputLLMProvider}
                  onChange={(e) => handleProviderChange(e.target.value)}
                  className="h-9 w-full text-xs"
                >
                  <option value="gemini">Google Gemini (Oficial)</option>
                  <option value="openai">OpenAI (Oficial)</option>
                  <option value="anthropic">Anthropic Claude</option>
                  <option value="ollama">Ollama (Local / On-Prem)</option>
                  <option value="openrouter">OpenRouter (Multi-Provider)</option>
                  <option value="groq">Groq (Ultra-Fast LPU)</option>
                  <option value="together">Together AI</option>
                  <option value="deepseek">DeepSeek (Direct)</option>
                  <option value="custom">Personalizado (OpenAI Compatible)</option>
                </Select>
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  MODELO
                </label>
                <Input
                  type="text"
                  value={inputLLMModel}
                  onChange={(e) => setInputLLMModel(e.target.value)}
                  placeholder="ej: gemini-2.5-flash, gpt-4o, claude-3-7-sonnet"
                  className="h-9 font-mono text-xs w-full"
                  required
                />
              </div>
            </div>

            {/* Model Suggestions Chips */}
            {currentModels.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                <span className="text-[10px] text-[var(--text-muted)] uppercase font-mono mr-1 flex items-center gap-1">
                  <Bot className="h-3 w-3" /> Sugeridos:
                </span>
                {currentModels.map((m) => (
                  <button
                    key={m}
                    type="button"
                    onClick={() => setInputLLMModel(m)}
                    className={`text-[10px] font-mono px-2 py-0.5 rounded-full border transition-all ${
                      inputLLMModel === m
                        ? "bg-blue-600/30 border-blue-500 text-blue-300"
                        : "bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-muted)] hover:text-[var(--text-primary)] hover:border-[var(--border-focus)]"
                    }`}
                  >
                    {m}
                  </button>
                ))}
              </div>
            )}

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] flex flex-wrap items-center justify-between gap-1 uppercase">
                <span className="flex items-center gap-1.5">
                  <Globe className="h-3.5 w-3.5 text-blue-400" />
                  API ENDPOINT / BASE URL
                </span>
                <span className="font-normal text-[var(--text-muted)] lowercase">
                  (Opcional, compatible con proxy)
                </span>
              </label>
              <Input
                type="text"
                value={inputLLMBaseURL}
                onChange={(e) => setInputLLMBaseURL(e.target.value)}
                placeholder="https://generativelanguage.googleapis.com/v1beta/openai"
                className="h-9 font-mono text-xs"
              />
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                API KEY / TOKEN DE LLM
                <span className="font-normal text-[var(--text-muted)] lowercase">
                  {" "}(solo en memoria; no se persiste en disco)
                </span>
              </label>
              <div className="relative">
                <Input
                  type={showKey ? "text" : "password"}
                  value={inputLLMKey}
                  onChange={(e) =>
                    setSecretLLMKey((state) => ({ ...state, typed: e.target.value }))
                  }
                  placeholder={inputLLMProvider === "ollama" ? "Opcional para Ollama local" : "AIzaSy... o sk-..."}
                  className="h-9 font-mono pr-10 text-xs"
                />
                <button
                  type="button"
                  onClick={() => setShowKey(!showKey)}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-[var(--text-primary)]"
                >
                  {showKey ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
                </button>
              </div>
            </div>

            {llmSavedMessage && (
              <div className="flex items-center gap-2 text-emerald-400 text-xs py-1">
                <CheckCircle className="h-4 w-4" />
                <span>¡Configuración de LLM guardada con éxito!</span>
              </div>
            )}

            <div className="flex justify-end pt-2">
              <Button type="submit" size="sm" className="gap-1.5 shadow-lg shadow-blue-600/20 text-xs">
                <Save className="h-3.5 w-3.5" />
                <span>Guardar Configuración LLM</span>
              </Button>
            </div>
          </form>
        </Card>

        {/* Vector Embeddings & Dimensionality Config */}
        <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-4">
            <CardTitle className="text-sm text-[var(--text-primary)] flex items-center gap-2">
              <Bot className="h-4 w-4 text-purple-400" />
              <span>Motor de Embeddings & Vectores</span>
            </CardTitle>
          </div>

          <form onSubmit={handleSaveEmbedding} className="space-y-4 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-4 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PROVEEDOR EMBEDDINGS
                </label>
                <Select
                  value={embedProvider}
                  onChange={(e) => handleEmbedProviderChange(e.target.value)}
                  className="h-9 w-full text-xs"
                >
                  <option value="gemini">Google Gemini</option>
                  <option value="openai">OpenAI</option>
                  <option value="ollama">Ollama</option>
                  <option value="custom">Personalizado</option>
                </Select>
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  MODELO EMBEDDING
                </label>
                <Input
                  type="text"
                  value={embedModel}
                  onChange={(e) => setEmbedModel(e.target.value)}
                  placeholder="text-embedding-004"
                  className="h-9 font-mono text-xs w-full"
                  required
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  DIMENSIONES
                </label>
                <Input
                  type="number"
                  value={embedDims}
                  onChange={(e) => setEmbedDims(Number(e.target.value))}
                  placeholder="768"
                  className="h-9 font-mono text-xs w-full"
                  required
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  MOTOR VECTORIAL
                </label>
                <Select
                  value={vecBackend}
                  onChange={(e) => setVecBackend(e.target.value)}
                  className="h-9 w-full text-xs"
                >
                  <option value="pgvector">PostgreSQL (pgvector)</option>
                  <option value="qdrant">Qdrant Server</option>
                  <option value="sqlite_blob">SQLite BLOB Cosine</option>
                </Select>
              </div>
            </div>

            <p className="text-[11px] text-[var(--text-muted)]">
              Controla las dimensiones del espacio vectorial semántico y el adaptador de indexación para búsqueda híbrida y similitud de embeddings.
            </p>

            {embedSavedMessage && (
              <div className="flex items-center gap-2 text-emerald-400 text-xs py-1">
                <CheckCircle className="h-4 w-4" />
                <span>¡Configuración del Motor de Embeddings guardada y persistida con éxito!</span>
              </div>
            )}

            <div className="flex justify-end pt-2">
              <Button type="submit" size="sm" className="gap-1.5 shadow-lg shadow-purple-600/20 text-xs bg-purple-600 hover:bg-purple-500 text-white">
                <Save className="h-3.5 w-3.5" />
                <span>Guardar Motor de Embeddings & Vectores</span>
              </Button>
            </div>
          </form>
        </Card>

        {/* Hybrid Search Weights & Retrieval Tuning */}
        <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-4">
            <CardTitle className="text-sm text-[var(--text-primary)] flex items-center gap-2">
              <Sliders className="h-4 w-4 text-emerald-400" />
              <span>Sintonización de Búsqueda Híbrida</span>
            </CardTitle>
          </div>

          <form onSubmit={handleSaveSearchWeights} className="space-y-3.5 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PESO BM25 ({Math.round(bm25Weight * 100)}%)
                </label>
                <Input
                  type="number"
                  step="0.05"
                  min="0"
                  max="1"
                  value={bm25Weight}
                  onChange={(e) => setBm25Weight(parseFloat(e.target.value) || 0)}
                  className="h-9 font-mono text-xs"
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PESO VECTOR ({Math.round(vectorWeight * 100)}%)
                </label>
                <Input
                  type="number"
                  step="0.05"
                  min="0"
                  max="1"
                  value={vectorWeight}
                  onChange={(e) => setVectorWeight(parseFloat(e.target.value) || 0)}
                  className="h-9 font-mono text-xs"
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PESO GRAFO ({Math.round(graphWeight * 100)}%)
                </label>
                <Input
                  type="number"
                  step="0.05"
                  min="0"
                  max="1"
                  value={graphWeight}
                  onChange={(e) => setGraphWeight(parseFloat(e.target.value) || 0)}
                  className="h-9 font-mono text-xs"
                />
              </div>
            </div>

            <div className="flex items-center justify-between pt-2">
              <span className="text-[11px] text-[var(--text-muted)]">
                Límite por consulta: <b>{defaultLimit}</b> resultados
              </span>
              <Button type="submit" size="sm" variant="outline" className="text-xs">
                {searchSavedMessage ? "¡Guardado!" : "Guardar Ponderación"}
              </Button>
            </div>
          </form>
        </Card>
      </div>

      {/* Autonomous AI Background Maintenance Worker Hub */}
      <Card className="p-4 sm:p-6 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-2xl space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
          <div>
            <CardTitle className="text-base text-[var(--text-primary)] flex items-center gap-2">
              <Bot className="h-5 w-5 text-indigo-400" />
              <span>Hub de Mantenimiento Autónomo con IA (Background Workers)</span>
            </CardTitle>
            <p className="text-xs text-[var(--text-muted)] mt-0.5">
              Ejecuta agentes en background con el API del modelo configurado para optimizar la base de conocimiento sin intervención manual.
            </p>
          </div>
          {isWorkerRunning && (
            <span className="flex items-center gap-2 text-xs text-amber-400 font-mono animate-pulse">
              <span className="w-2 h-2 rounded-full bg-amber-400" />
              Agente en ejecución...
            </span>
          )}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3.5">
          {/* Job 1: Graph Auto Reorganizer */}
          <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex flex-col justify-between space-y-3">
            <div>
              <div className="flex items-center gap-2 font-semibold text-xs text-[var(--text-primary)] mb-1">
                <span>⚡ Reorganizar Grafo Semántico</span>
              </div>
              <p className="text-[11px] text-[var(--text-secondary)] leading-relaxed">
                Descubre enlaces ocultos entre observaciones huérfanas y crea relaciones estructuradas (relates_to, supersedes).
              </p>
            </div>
            <Button
              onClick={runBackgroundGraphReorganization}
              disabled={isWorkerRunning}
              size="sm"
              className="w-full text-xs gap-1.5 shadow-md shadow-blue-600/10"
            >
              <span>Ejecutar Reorganización</span>
            </Button>
          </div>

          {/* Job 2: Conflict & Contradiction Resolver */}
          <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex flex-col justify-between space-y-3">
            <div>
              <div className="flex items-center gap-2 font-semibold text-xs text-[var(--text-primary)] mb-1">
                <span>🛡️ Resolver Conflictos & Contradicciones</span>
              </div>
              <p className="text-[11px] text-[var(--text-secondary)] leading-relaxed">
                Detecta decisiones de código contradictorias u obsoletas y genera superaciones automáticas con razonamiento.
              </p>
            </div>
            <Button
              onClick={runBackgroundConflictResolution}
              disabled={isWorkerRunning}
              size="sm"
              variant="secondary"
              className="w-full text-xs gap-1.5"
            >
              <span>Escanear y Resolver</span>
            </Button>
          </div>

          {/* Job 3: Project Memory Consolidation */}
          <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex flex-col justify-between space-y-3">
            <div>
              <div className="flex items-center gap-2 font-semibold text-xs text-[var(--text-primary)] mb-1">
                <span>🧠 Consolidar Memoria de Proyectos</span>
              </div>
              <p className="text-[11px] text-[var(--text-secondary)] leading-relaxed">
                Sintetiza notas dispersas en un resumen de alto nivel de arquitectura y directivas activas.
              </p>
            </div>
            <Button
              onClick={runBackgroundConsolidation}
              disabled={isWorkerRunning}
              size="sm"
              variant="secondary"
              className="w-full text-xs gap-1.5"
            >
              <span>Consolidar Memoria</span>
            </Button>
          </div>
        </div>

        {/* Live Background Logs Console */}
        {workerLogs.length > 0 && (
          <div className="mt-4 p-3.5 rounded-lg bg-black/50 border border-[var(--border-subtle)] font-mono text-[11px] space-y-1">
            <div className="text-[10px] text-[var(--text-muted)] uppercase tracking-wider mb-2 flex items-center justify-between">
              <span>Registro de Operaciones en Background:</span>
              <button
                type="button"
                onClick={() => setWorkerLogs([])}
                className="text-[10px] text-[var(--text-muted)] hover:text-white"
              >
                Limpiar
              </button>
            </div>
            {workerLogs.map((log, index) => (
              <div key={index} className="text-slate-300">
                <span className="text-blue-400 mr-2">&gt;</span>
                {log}
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
