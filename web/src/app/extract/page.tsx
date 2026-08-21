"use client";

import React, { useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { ExtractionResult, ExtractedObservation, ExtractedEdge, SynthesisResult } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
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
  const {
    client,
    llmApiKey,
    llmProvider,
    llmModel,
    llmBaseURL,
    setLLMCredentials,
  } = useAuth();

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
  const [baseURLInput, setBaseURLInput] = useState(llmBaseURL || "");
  const [saveSuccessMessage, setSaveSuccessMessage] = useState<string | null>(null);

  const handleSaveLLMSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setLLMCredentials(apiKeyInput, providerInput, modelInput, baseURLInput);
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
        llm_config: (apiKeyInput || baseURLInput || providerInput === "ollama") ? {
          provider: providerInput,
          api_key: apiKeyInput,
          model: modelInput,
          base_url: baseURLInput || undefined,
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
        llm_config: (apiKeyInput || baseURLInput || providerInput === "ollama") ? {
          provider: providerInput,
          api_key: apiKeyInput,
          model: modelInput,
          base_url: baseURLInput || undefined,
        } : undefined,
      });
      setSynthesisResult(res);
    } catch (err: any) {
      alert("Error al sintetizar: " + (err.message || err));
    } finally {
      setIsExtracting(false);
    }
  };

  const handleProviderSelect = (p: string) => {
    setProviderInput(p);
    if (p === "openai") {
      setBaseURLInput("https://api.openai.com/v1");
      setModelInput("gpt-4o-mini");
    } else if (p === "anthropic") {
      setBaseURLInput("https://api.anthropic.com/v1");
      setModelInput("claude-3-5-sonnet-20241022");
    } else if (p === "ollama") {
      setBaseURLInput("http://localhost:11434/v1");
      setModelInput("llama3.3");
    } else if (p === "openrouter") {
      setBaseURLInput("https://openrouter.ai/api/v1");
      setModelInput("openai/gpt-4o-mini");
    } else if (p === "groq") {
      setBaseURLInput("https://api.groq.com/openai/v1");
      setModelInput("llama-3.3-70b-versatile");
    } else if (p === "together") {
      setBaseURLInput("https://api.together.xyz/v1");
      setModelInput("meta-llama/Llama-3.3-70B-Instruct-Turbo");
    } else if (p === "deepseek") {
      setBaseURLInput("https://api.deepseek.com/v1");
      setModelInput("deepseek-chat");
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white flex items-center gap-2.5">
            <Sparkles className="h-6 w-6 text-blue-500" />
            Extracción y Síntesis Automática con IA
          </h1>
          <p className="text-xs text-slate-400 mt-1">
            Extrae observaciones estructuradas, entidades y relaciones desde transcripciones de sesiones o notas de código
          </p>
        </div>

        <Button
          onClick={() => setShowLLMSettings(!showLLMSettings)}
          variant="secondary"
          size="sm"
          className="gap-2"
        >
          <Settings className="h-4 w-4" />
          <span>{apiKeyInput || baseURLInput ? "LLM Configurado" : "Configurar LLM (API / Token)"}</span>
        </Button>
      </div>

      {/* LLM Settings Collapsible Card */}
      {showLLMSettings && (
        <Card className="p-5 bg-slate-900/80 border-slate-800 shadow-xl">
          <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
            <CardTitle className="text-sm">
              <Key className="h-4 w-4 text-blue-400" />
              Configuración de Proveedor LLM Personalizado
            </CardTitle>
          </div>

          <form onSubmit={handleSaveLLMSettings} className="space-y-3 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  PROVEEDOR
                </label>
                <Select
                  value={providerInput}
                  onChange={(e) => handleProviderSelect(e.target.value)}
                  className="h-9 text-xs"
                >
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic Claude</option>
                  <option value="ollama">Ollama (Local)</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="groq">Groq LPU</option>
                  <option value="together">Together AI</option>
                  <option value="deepseek">DeepSeek</option>
                  <option value="custom">Personalizado (OpenAI-compatible)</option>
                </Select>
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  MODELO
                </label>
                <Input
                  type="text"
                  placeholder="gpt-4o-mini / llama3.3 / claude-3-5-sonnet"
                  value={modelInput}
                  onChange={(e) => setModelInput(e.target.value)}
                  className="h-9 text-xs font-mono"
                  required
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  API ENDPOINT / BASE URL
                </label>
                <Input
                  type="text"
                  placeholder="https://api.openai.com/v1"
                  value={baseURLInput}
                  onChange={(e) => setBaseURLInput(e.target.value)}
                  className="h-9 text-xs font-mono"
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                  API KEY / TOKEN
                </label>
                <Input
                  type="password"
                  placeholder={providerInput === "ollama" ? "Opcional (Ollama)" : "sk-..."}
                  value={apiKeyInput}
                  onChange={(e) => setApiKeyInput(e.target.value)}
                  className="h-9 text-xs font-mono"
                />
              </div>
            </div>

            <div className="flex items-center justify-between pt-1">
              <span className="text-[11px] text-slate-500">
                Si no configuras credenciales, Cortex ejecutará el extractor heurístico integrado.
              </span>
              <Button type="submit" size="sm" className="h-8">
                Guardar Configuración
              </Button>
            </div>
          </form>
        </Card>
      )}

      {/* Input Section */}
      <Card className="p-5 bg-slate-900/70 border-slate-800 shadow-xl space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <label className="text-xs font-semibold text-slate-200">
            Transcripción de Sesión, Notas de Arquitectura o Logs de Código:
          </label>
          <div className="flex items-center gap-2">
            <span className="text-xs text-slate-400">Proyecto:</span>
            <Input
              type="text"
              value={project}
              onChange={(e) => setProject(e.target.value)}
              className="w-40 h-8 text-xs bg-slate-950/80"
            />
          </div>
        </div>

        <textarea
          className="flex min-h-[140px] w-full rounded-lg border border-slate-700 bg-slate-950/80 px-3.5 py-2.5 text-xs text-slate-100 placeholder:text-slate-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500 font-mono"
          rows={7}
          placeholder="Pega aquí el registro de una sesión, decisiones tomadas, explicaciones de bugfixes o notas técnicas..."
          value={rawText}
          onChange={(e) => setRawText(e.target.value)}
        />

        <div className="flex justify-end gap-2.5 pt-1">
          <Button
            onClick={handleSynthesize}
            variant="secondary"
            size="sm"
            disabled={isExtracting}
            className="gap-2"
          >
            <Layers className="h-4 w-4" />
            <span>Sintetizar Proyecto</span>
          </Button>

          <Button
            onClick={handleExtract}
            size="sm"
            disabled={isExtracting || !rawText.trim()}
            className="gap-2 shadow-lg shadow-blue-600/20"
          >
            <Sparkles className="h-4 w-4" />
            <span>{isExtracting ? "Analizando y Extrayendo..." : "Extraer Conocimiento con IA"}</span>
          </Button>
        </div>
      </Card>

      {saveSuccessMessage && (
        <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 p-4 rounded-xl text-xs flex items-center gap-2.5">
          <CheckCircle className="h-5 w-5 shrink-0" />
          <span>{saveSuccessMessage}</span>
        </div>
      )}

      {/* Extraction Results */}
      {extractionResult && (
        <div className="space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 className="text-base font-semibold text-white">
                Resultado de la Extracción ({extractionResult.source_method.toUpperCase()})
              </h2>
              <p className="text-xs text-slate-400">
                {extractionResult.summary}
              </p>
            </div>

            <Button
              onClick={handleSaveToCortex}
              size="sm"
              disabled={isSaving}
              className="gap-2 shadow-lg shadow-blue-600/20"
            >
              <Database className="h-4 w-4" />
              <span>{isSaving ? "Guardando..." : "Guardar Todo en Cortex"}</span>
            </Button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {extractionResult.observations.map((obs, idx) => (
              <Card key={idx} className="p-4 bg-slate-900/80 border-slate-800 space-y-2">
                <div className="flex items-start justify-between gap-2">
                  <h3 className="text-xs font-semibold text-slate-100 line-clamp-1">
                    {obs.title}
                  </h3>
                  <Badge variant={obs.type === "decision" ? "default" : obs.type === "bugfix" ? "destructive" : "secondary"}>
                    {obs.type}
                  </Badge>
                </div>
                <p className="text-xs text-slate-300 line-clamp-3 leading-relaxed">
                  {obs.content}
                </p>
                {obs.tags && obs.tags.length > 0 && (
                  <div className="flex flex-wrap gap-1 pt-1">
                    {obs.tags.map((t, i) => (
                      <span key={i} className="text-[10px] px-1.5 py-0.5 rounded bg-slate-950 border border-slate-800 text-slate-400 font-mono">
                        #{t}
                      </span>
                    ))}
                  </div>
                )}
              </Card>
            ))}
          </div>

          {extractionResult.edges.length > 0 && (
            <Card className="p-5 bg-slate-900/80 border-slate-800 space-y-3">
              <CardTitle className="text-sm">
                <Share2 className="h-4 w-4 text-blue-400" />
                Relaciones Detectadas para el Grafo
              </CardTitle>
              <div className="space-y-2">
                {extractionResult.edges.map((e, idx) => (
                  <div
                    key={idx}
                    className="p-3 bg-slate-950/70 rounded-lg border border-slate-800/80 text-xs flex flex-wrap items-center gap-2.5"
                  >
                    <span className="font-semibold text-slate-200">{e.from_title}</span>
                    <Badge variant="default" className="text-[10px]">{e.relation_type}</Badge>
                    <span className="font-semibold text-slate-200">{e.to_title}</span>
                    <span className="text-slate-500 ml-auto text-[11px]">{e.reasoning}</span>
                  </div>
                ))}
              </div>
            </Card>
          )}
        </div>
      )}

      {/* Synthesis Results */}
      {synthesisResult && (
        <Card className="p-6 bg-slate-900/80 border-slate-800 space-y-4">
          <div>
            <h2 className="text-base font-bold text-white">
              Mapa de Conocimiento Consolidado: {synthesisResult.project}
            </h2>
            <p className="text-xs text-slate-400 mt-1">
              {synthesisResult.summary}
            </p>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-2 gap-5 pt-2">
            <div className="space-y-2">
              <h3 className="text-xs font-semibold text-slate-200 uppercase tracking-wider">
                Decisiones Clave
              </h3>
              <ul className="space-y-1.5 pl-4 text-xs text-slate-300 list-disc">
                {synthesisResult.key_decisions.map((d, i) => (
                  <li key={i}>{d}</li>
                ))}
              </ul>
            </div>

            <div className="space-y-2">
              <h3 className="text-xs font-semibold text-slate-200 uppercase tracking-wider">
                Patrones y Estándares
              </h3>
              <ul className="space-y-1.5 pl-4 text-xs text-slate-300 list-disc">
                {synthesisResult.patterns.map((p, i) => (
                  <li key={i}>{p}</li>
                ))}
              </ul>
            </div>
          </div>
        </Card>
      )}
    </div>
  );
}
