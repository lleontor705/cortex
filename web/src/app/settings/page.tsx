"use client";

import React, { useState } from "react";
import { useAuth } from "@/lib/auth-context";
import {
  Settings,
  Server,
  Key,
  Sparkles,
  CheckCircle,
  Save,
  LogOut,
  Shield,
} from "lucide-react";

export default function SettingsPage() {
  const {
    serverUrl,
    token,
    llmApiKey,
    llmProvider,
    llmModel,
    setCredentials,
    setLLMCredentials,
    logout,
  } = useAuth();

  const [inputUrl, setInputUrl] = useState(serverUrl);
  const [inputToken, setInputToken] = useState(token);

  const [inputLLMKey, setInputLLMKey] = useState(llmApiKey);
  const [inputLLMProvider, setInputLLMProvider] = useState(llmProvider);
  const [inputLLMModel, setInputLLMModel] = useState(llmModel);

  const [serverSavedMessage, setServerSavedMessage] = useState(false);
  const [llmSavedMessage, setLlmSavedMessage] = useState(false);

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
    setLLMCredentials(inputLLMKey, inputLLMProvider, inputLLMModel);
    setLlmSavedMessage(true);
    setTimeout(() => setLlmSavedMessage(false), 3000);
  };

  return (
    <div>
      <div style={{ marginBottom: "24px" }}>
        <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
          Configuración del Sistema
        </h1>
        <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
          Ajustes de conexión al servidor Cortex, credenciales de autenticación y proveedores de modelos de lenguaje
        </p>
      </div>

      <div style={{ display: "flex", flexDirection: "column", gap: "24px", maxWidth: "800px" }}>
        {/* Server Connection Settings */}
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <Server size={18} />
              Conexión al Servidor Cortex
            </h2>
          </div>

          <form onSubmit={handleSaveServer} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
            <div>
              <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                ENDPOINT URL
              </label>
              <input
                type="text"
                className="input"
                value={inputUrl}
                onChange={(e) => setInputUrl(e.target.value)}
                required
              />
            </div>

            <div>
              <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                BEARER TOKEN
              </label>
              <input
                type="password"
                className="input"
                value={inputToken}
                onChange={(e) => setInputToken(e.target.value)}
                required
              />
            </div>

            {serverSavedMessage && (
              <div style={{ display: "flex", alignItems: "center", gap: "6px", color: "var(--success)", fontSize: "13px" }}>
                <CheckCircle size={15} />
                <span>¡Conexión y credenciales actualizadas con éxito!</span>
              </div>
            )}

            <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "4px" }}>
              <button type="button" onClick={logout} className="btn btn-danger">
                <LogOut size={14} />
                <span>Cerrar Sesión</span>
              </button>
              <button type="submit" className="btn btn-primary">
                <Save size={14} />
                <span>Guardar Cambios</span>
              </button>
            </div>
          </form>
        </div>

        {/* LLM Engine Settings (Mejora 4.1) */}
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <Sparkles size={18} color="var(--accent-primary)" />
              Motor de Extracción Semántica (LLM)
            </h2>
          </div>

          <form onSubmit={handleSaveLLM} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  PROVEEDOR
                </label>
                <select
                  className="select"
                  value={inputLLMProvider}
                  onChange={(e) => setInputLLMProvider(e.target.value)}
                >
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="ollama">Ollama (Local)</option>
                  <option value="openrouter">OpenRouter</option>
                </select>
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  MODELO
                </label>
                <input
                  type="text"
                  className="input"
                  value={inputLLMModel}
                  onChange={(e) => setInputLLMModel(e.target.value)}
                  placeholder="gpt-4o-mini"
                />
              </div>
            </div>

            <div>
              <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                API KEY / BEARER TOKEN DE LLM
              </label>
              <input
                type="password"
                className="input"
                value={inputLLMKey}
                onChange={(e) => setInputLLMKey(e.target.value)}
                placeholder="sk-..."
              />
            </div>

            {llmSavedMessage && (
              <div style={{ display: "flex", alignItems: "center", gap: "6px", color: "var(--success)", fontSize: "13px" }}>
                <CheckCircle size={15} />
                <span>¡Configuración de LLM guardada con éxito!</span>
              </div>
            )}

            <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "4px" }}>
              <button type="submit" className="btn btn-primary">
                <Save size={14} />
                <span>Guardar Configuración LLM</span>
              </button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
}
