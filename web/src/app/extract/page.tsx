"use client";

import React, { useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { ExtractionResult, ExtractedObservation, ExtractedEdge, SynthesisResult } from "@/lib/api";
import {
  Sparkles,
  Layers,
  CheckCircle,
  Key,
  Settings,
  ArrowRight,
  Database,
  Tag,
  Share2,
  FileText,
  AlertCircle,
} from "lucide-react";

export default function ExtractPage() {
  const { client, llmApiKey, llmProvider, llmModel, setLLMCredentials } = useAuth();

  const [rawText, setRawText] = useState("");
  const [project, setProject] = useState("default");
  const [isExtracting, setIsExtracting] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [extractionResult, setExtractionResult] = useState<ExtractionResult | null>(null);
  const [synthesisResult, setSynthesisResult] = useState<SynthesisResult | null>(null);

  // LLM Config in page
  const [showLLMSettings, setShowLLMSettings] = useState(false);
  const [apiKeyInput, setApiKeyInput] = useState(llmApiKey);
  const [providerInput, setProviderInput] = useState(llmProvider || "openai");
  const [modelInput, setModelInput] = useState(llmModel || "gpt-4o-mini");
  const [saveSuccessMessage, setSaveSuccessMessage] = useState<string | null>(null);

  const handleSaveLLMSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setLLMCredentials(apiKeyInput, providerInput, modelInput);
    setShowLLMSettings(false);
  };

  const handleExtract = async () => {
    if (!client || !rawText.trim()) return;
    setIsExtracting(true);
    setExtractionResult(null);
    setSynthesisResult(null);
    setSaveSuccessMessage(null);

    try {
      const res = await client.extract({
        text: rawText,
        project: project,
        llm_config: apiKeyInput ? {
          provider: providerInput,
          api_key: apiKeyInput,
          model: modelInput,
        } : undefined,
      });
      setExtractionResult(res);
    } catch (err: any) {
      alert("Error en la extracción: " + (err.message || err));
    } finally {
      setIsExtracting(false);
    }
  };

  const handleSaveToCortex = async () => {
    if (!client || !extractionResult) return;
    setIsSaving(true);
    try {
      const savedObs: any[] = [];
      for (const draft of extractionResult.observations) {
        const o = await client.createObservation({
          title: draft.title,
          content: draft.content,
          type: draft.type,
          project: draft.project || project,
          scope: draft.scope || "project",
          tags: draft.tags,
          confidence: draft.confidence || 0.9,
        });
        savedObs.push(o);
      }

      // Save edges if we have matching titles
      for (const edge of extractionResult.edges) {
        const fromObs = savedObs.find((o) => o.title === edge.from_title);
        const toObs = savedObs.find((o) => o.title === edge.to_title);
        if (fromObs && toObs && fromObs.id && toObs.id) {
          await client.createEdge({
            from_id: fromObs.id,
            to_id: toObs.id,
            relation_type: edge.relation_type || "relates_to",
            confidence: edge.confidence || 0.8,
            reasoning: edge.reasoning,
          }).catch(() => null);
        }
      }

      setSaveSuccessMessage(`¡Se guardaron ${savedObs.length} observaciones y sus relaciones en Cortex con éxito!`);
    } catch (err: any) {
      alert("Error al guardar en Cortex: " + (err.message || err));
    } finally {
      setIsSaving(false);
    }
  };

  const handleSynthesize = async () => {
    if (!client) return;
    setIsExtracting(true);
    try {
      const obsList = await client.listObservations(`?project=${encodeURIComponent(project)}&limit=20`);
      if (!obsList || obsList.length === 0) {
        alert("No hay observaciones en este proyecto para sintetizar.");
        return;
      }
      const res = await client.synthesize({
        project,
        observations: obsList,
        llm_config: apiKeyInput ? {
          provider: providerInput,
          api_key: apiKeyInput,
          model: modelInput,
        } : undefined,
      });
      setSynthesisResult(res);
    } catch (err: any) {
      alert("Error al sintetizar: " + (err.message || err));
    } finally {
      setIsExtracting(false);
    }
  };

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "24px", flexWrap: "wrap", gap: "16px" }}>
        <div>
          <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
            Extracción y Síntesis Automática con IA
          </h1>
          <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
            Extrae observaciones estructuradas, entidades y relaciones desde transcripciones de sesiones o notas de código
          </p>
        </div>

        <button
          onClick={() => setShowLLMSettings(!showLLMSettings)}
          className="btn btn-secondary"
        >
          <Settings size={15} />
          <span>{apiKeyInput ? "LLM Configurado" : "Configurar LLM (API Key)"}</span>
        </button>
      </div>

      {/* LLM Settings Collapsible Card */}
      {showLLMSettings && (
        <div className="card" style={{ marginBottom: "24px", borderColor: "var(--border-default)" }}>
          <div className="card-header">
            <h2 className="card-title">
              <Key size={17} color="var(--accent-primary)" />
              Configuración de Proveedor LLM para Extracción
            </h2>
          </div>

          <form onSubmit={handleSaveLLMSettings} style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr auto", gap: "12px", alignItems: "flex-end" }}>
            <div>
              <label style={{ display: "block", fontSize: "11px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                PROVEEDOR
              </label>
              <select
                className="select"
                value={providerInput}
                onChange={(e) => setProviderInput(e.target.value)}
              >
                <option value="openai">OpenAI (o compatible)</option>
                <option value="anthropic">Anthropic Claude</option>
                <option value="ollama">Ollama Local</option>
                <option value="openrouter">OpenRouter</option>
              </select>
            </div>

            <div>
              <label style={{ display: "block", fontSize: "11px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                MODELO
              </label>
              <input
                type="text"
                className="input"
                placeholder="gpt-4o-mini / claude-3-5-sonnet"
                value={modelInput}
                onChange={(e) => setModelInput(e.target.value)}
              />
            </div>

            <div>
              <label style={{ display: "block", fontSize: "11px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                API KEY / TOKEN
              </label>
              <input
                type="password"
                className="input"
                placeholder="sk-..."
                value={apiKeyInput}
                onChange={(e) => setApiKeyInput(e.target.value)}
              />
            </div>

            <button type="submit" className="btn btn-primary">
              Guardar
            </button>
          </form>
          <div style={{ fontSize: "11px", color: "var(--text-muted)", marginTop: "8px" }}>
            Si no configuras una API Key, Cortex usará el extractor heurístico determinista integrado.
          </div>
        </div>
      )}

      {/* Input Section */}
      <div className="card" style={{ marginBottom: "24px" }}>
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "12px" }}>
          <label style={{ fontSize: "13px", fontWeight: "600", color: "var(--text-primary)" }}>
            Transcripción de Sesión, Notas de Arquitectura o Logs de Código:
          </label>
          <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
            <span style={{ fontSize: "12px", color: "var(--text-muted)" }}>Proyecto:</span>
            <input
              type="text"
              className="input"
              style={{ width: "160px", padding: "4px 8px", fontSize: "12px" }}
              value={project}
              onChange={(e) => setProject(e.target.value)}
            />
          </div>
        </div>

        <textarea
          className="textarea"
          rows={7}
          placeholder="Pega aquí el registro de una sesión, decisiones tomadas, explicaciones de bugfixes o notas técnicas..."
          value={rawText}
          onChange={(e) => setRawText(e.target.value)}
        />

        <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "14px" }}>
          <button
            onClick={handleSynthesize}
            className="btn btn-secondary"
            disabled={isExtracting}
          >
            <Layers size={15} />
            <span>Sintetizar Proyecto</span>
          </button>

          <button
            onClick={handleExtract}
            className="btn btn-primary"
            disabled={isExtracting || !rawText.trim()}
          >
            <Sparkles size={15} />
            <span>{isExtracting ? "Analizando y Extrayendo..." : "Extraer Conocimiento con IA"}</span>
          </button>
        </div>
      </div>

      {saveSuccessMessage && (
        <div
          style={{
            backgroundColor: "var(--success-bg)",
            border: "1px solid rgba(16, 185, 129, 0.3)",
            color: "var(--success)",
            padding: "14px 18px",
            borderRadius: "var(--radius-md)",
            fontSize: "13px",
            marginBottom: "24px",
            display: "flex",
            alignItems: "center",
            gap: "10px",
          }}
        >
          <CheckCircle size={18} />
          <span>{saveSuccessMessage}</span>
        </div>
      )}

      {/* Extraction Results */}
      {extractionResult && (
        <div>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "16px" }}>
            <div>
              <h2 style={{ fontSize: "17px", fontWeight: "600" }}>
                Resultado de la Extracción ({extractionResult.source_method.toUpperCase()})
              </h2>
              <p style={{ color: "var(--text-secondary)", fontSize: "12px" }}>
                {extractionResult.summary}
              </p>
            </div>

            <button
              onClick={handleSaveToCortex}
              className="btn btn-primary"
              disabled={isSaving}
            >
              <Database size={15} />
              <span>{isSaving ? "Guardando..." : "Guardar Todo en Cortex"}</span>
            </button>
          </div>

          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(340px, 1fr))", gap: "16px", marginBottom: "24px" }}>
            {extractionResult.observations.map((obs, idx) => (
              <div key={idx} className="card" style={{ padding: "16px" }}>
                <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "8px" }}>
                  <h3 style={{ fontSize: "14px", fontWeight: "600", color: "var(--text-primary)" }}>
                    {obs.title}
                  </h3>
                  <span className={`badge ${obs.type === "decision" ? "badge-blue" : obs.type === "bugfix" ? "badge-amber" : "badge-zinc"}`}>
                    {obs.type}
                  </span>
                </div>
                <p style={{ fontSize: "12px", color: "var(--text-secondary)", marginBottom: "10px", lineHeight: "1.5" }}>
                  {obs.content}
                </p>
                {obs.tags && obs.tags.length > 0 && (
                  <div style={{ display: "flex", gap: "4px", flexWrap: "wrap" }}>
                    {obs.tags.map((t, i) => (
                      <span key={i} className="badge badge-zinc" style={{ fontSize: "10px" }}>
                        #{t}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>

          {extractionResult.edges.length > 0 && (
            <div className="card" style={{ padding: "18px" }}>
              <h3 style={{ fontSize: "14px", fontWeight: "600", marginBottom: "12px", display: "flex", alignItems: "center", gap: "6px" }}>
                <Share2 size={16} color="var(--accent-primary)" />
                Relaciones Detectadas para el Grafo
              </h3>
              <div style={{ display: "flex", flexDirection: "column", gap: "8px" }}>
                {extractionResult.edges.map((e, idx) => (
                  <div
                    key={idx}
                    style={{
                      padding: "8px 12px",
                      backgroundColor: "var(--bg-input)",
                      borderRadius: "var(--radius-sm)",
                      fontSize: "12px",
                      display: "flex",
                      alignItems: "center",
                      gap: "10px",
                    }}
                  >
                    <span style={{ fontWeight: "600" }}>{e.from_title}</span>
                    <span className="badge badge-blue">{e.relation_type}</span>
                    <span style={{ fontWeight: "600" }}>{e.to_title}</span>
                    <span style={{ color: "var(--text-muted)", marginLeft: "auto" }}>{e.reasoning}</span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Synthesis Results */}
      {synthesisResult && (
        <div className="card" style={{ padding: "22px" }}>
          <h2 style={{ fontSize: "18px", fontWeight: "700", marginBottom: "8px" }}>
            Mapa de Conocimiento Consolidado: {synthesisResult.project}
          </h2>
          <p style={{ color: "var(--text-secondary)", fontSize: "13px", marginBottom: "18px" }}>
            {synthesisResult.summary}
          </p>

          <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "18px" }}>
            <div>
              <h3 style={{ fontSize: "13px", fontWeight: "600", color: "var(--text-primary)", marginBottom: "8px" }}>
                Decisiones Clave
              </h3>
              <ul style={{ paddingLeft: "18px", color: "var(--text-secondary)", fontSize: "12px", display: "flex", flexDirection: "column", gap: "4px" }}>
                {synthesisResult.key_decisions.map((d, i) => (
                  <li key={i}>{d}</li>
                ))}
              </ul>
            </div>

            <div>
              <h3 style={{ fontSize: "13px", fontWeight: "600", color: "var(--text-primary)", marginBottom: "8px" }}>
                Patrones y Estándares
              </h3>
              <ul style={{ paddingLeft: "18px", color: "var(--text-secondary)", fontSize: "12px", display: "flex", flexDirection: "column", gap: "4px" }}>
                {synthesisResult.patterns.map((p, i) => (
                  <li key={i}>{p}</li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
