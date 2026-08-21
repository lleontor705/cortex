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

export default function SettingsPage() {
  const {
    serverUrl,
    token,
    resetGeneration,
    llmApiKey,
    llmProvider,
    llmModel,
    llmBaseURL,
    setCredentials,
    setLLMCredentials,
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

  const [inputLLMProvider, setInputLLMProvider] = useState(llmProvider || "openai");
  const [inputLLMModel, setInputLLMModel] = useState(llmModel || "gpt-4o-mini");
  const [inputLLMBaseURL, setInputLLMBaseURL] = useState(llmBaseURL || "");
  const [showKey, setShowKey] = useState(false);
  const [showBearer, setShowBearer] = useState(false);

  const [serverSavedMessage, setServerSavedMessage] = useState(false);
  const [llmSavedMessage, setLlmSavedMessage] = useState(false);

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

  const currentModels = PROVIDER_DEFAULTS[inputLLMProvider]?.models || [];

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-slate-100 flex items-center gap-2">
          <Sliders className="h-6 w-6 text-blue-500" />
          Configuración del Sistema
        </h1>
        <p className="text-sm text-slate-400">
          Gestiona el endpoint de Cortex Server y la integración con proveedores LLM personalizados.
        </p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 items-start">
        {/* Cortex Server Connection Settings */}
        <Card className="p-5 bg-slate-900/70 border-slate-800 shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
            <CardTitle className="text-sm">
              <Server className="h-4 w-4 text-blue-400" />
              Conexión Cortex Server
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
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
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
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                BEARER TOKEN
                <span className="font-normal text-slate-500 lowercase">
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
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-200"
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
              <Button type="submit" size="sm" className="gap-1.5 shadow-lg shadow-blue-600/20">
                <Save className="h-3.5 w-3.5" />
                <span>Actualizar Conexión</span>
              </Button>
            </div>
          </form>
        </Card>

        {/* LLM Engine Settings with Full Custom Support */}
        <Card className="p-5 bg-slate-900/70 border-slate-800 shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
            <CardTitle className="text-sm">
              <Sparkles className="h-4 w-4 text-blue-400" />
              Proveedor de LLM Personalizado
            </CardTitle>
          </div>

          <form onSubmit={handleSaveLLM} className="space-y-4 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  PROVEEDOR
                </label>
                <Select
                  value={inputLLMProvider}
                  onChange={(e) => handleProviderChange(e.target.value)}
                  className="h-9"
                >
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
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  MODELO
                </label>
                <Input
                  type="text"
                  value={inputLLMModel}
                  onChange={(e) => setInputLLMModel(e.target.value)}
                  placeholder="ej: gpt-4o, claude-3-7-sonnet, llama3.3"
                  className="h-9 font-mono"
                  required
                />
              </div>
            </div>

            {/* Model Suggestions Chips */}
            {currentModels.length > 0 && (
              <div className="flex flex-wrap items-center gap-1.5 pt-0.5">
                <span className="text-[10px] text-slate-500 uppercase font-mono mr-1 flex items-center gap-1">
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
                        : "bg-slate-800/80 border-slate-700 text-slate-400 hover:text-slate-200 hover:border-slate-600"
                    }`}
                  >
                    {m}
                  </button>
                ))}
              </div>
            )}

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-slate-300 flex items-center justify-between uppercase">
                <span className="flex items-center gap-1.5">
                  <Globe className="h-3.5 w-3.5 text-blue-400" />
                  API ENDPOINT / BASE URL
                </span>
                <span className="font-normal text-slate-500 lowercase">
                  (Opcional, compatible con proxy corporativo)
                </span>
              </label>
              <Input
                type="text"
                value={inputLLMBaseURL}
                onChange={(e) => setInputLLMBaseURL(e.target.value)}
                placeholder="https://api.openai.com/v1 o http://localhost:11434/v1"
                className="h-9 font-mono"
              />
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                API KEY / TOKEN DE LLM
                <span className="font-normal text-slate-500 lowercase">
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
                  placeholder={inputLLMProvider === "ollama" ? "Opcional para Ollama local" : "sk-..."}
                  className="h-9 font-mono pr-10"
                />
                <button
                  type="button"
                  onClick={() => setShowKey(!showKey)}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-200"
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
              <Button type="submit" size="sm" className="gap-1.5 shadow-lg shadow-blue-600/20">
                <Save className="h-3.5 w-3.5" />
                <span>Guardar Configuración LLM</span>
              </Button>
            </div>
          </form>
        </Card>
      </div>
    </div>
  );
}
