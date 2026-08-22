"use client";

import React, { useState, useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  ExtractionResult,
  ExtractedObservation,
  ExtractedEdge,
  SynthesisResult,
} from "@/lib/api";
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
  Copy,
  Check,
  BookOpen,
  Code2,
  Bug,
  Cpu,
  Download,
  Terminal,
  Upload,
  RefreshCw,
  FolderKanban,
  CheckSquare,
  Square,
  HelpCircle,
  ExternalLink,
} from "lucide-react";

type DraftItem = ExtractedObservation & {
  selected: boolean;
};

const TEMPLATES = {
  bugfix: {
    label: "🐛 Post-Mortem / Fix de Bug Complejo",
    text: `[INCIDENTE]: Falla de saturación de conexiones en el pool de PostgreSQL.
[CAUSA RAÍZ]: Los microservicios no liberaban las conexiones transaccionales al ocurrir errores no manejados en streaming de eventos.
[DECISIÓN TÉCNICA]: Se configuró PgBouncer en modo 'transaction pooling' con un timeout de 30 segundos y pool_size=25 en internal/platform/server.
[PATRÓN OBLIGATORIO]: Todas las operaciones de escritura deben ejecutarse a través de s.store.transaction(ctx) garantizando rollback inmediato ante cualquier error.`,
  },
  architecture: {
    label: "🏗️ Decisión de Arquitectura / RFC",
    text: `[DECISIÓN DE ARQUITECTURA]: Adopción de Zero-CGO y Clean Architecture en el runtime local.
[CONTEXTO]: Cortex debe ejecutarse de forma portátil en Windows, Linux y macOS sin requerir gcc ni compiladores externos.
[REGLAS ESTABLECIDAS]:
1. internal/domain contiene los puertos, entidades y reglas de negocio puras.
2. internal/store/sqlite implementa la persistencia local con modernc.org/sqlite.
3. El runtime local nunca importa PostgreSQL ni dependencias de servidor (internal/platform/server).`,
  },
  pr_log: {
    label: "🚀 Pull Request / Log de Cambios",
    text: `PR #84: Protocolo de Jerarquía Dinámica de Reglas & Skills MCP
- Se añadió endpoint GET /api/projects/context para alimentar los system prompts de agentes MCP.
- Las directivas globales de Workspace se fusionan con las directivas específicas de cada proyecto.
- Se implementó autorización BOLA estricta impidiendo que usuarios Developer eliminen observaciones de otros desarrolladores.`,
  },
};

export default function ExtractPage() {
  const router = useRouter();
  const {
    client,
    llmApiKey,
    llmProvider,
    llmModel,
    llmBaseURL,
    setLLMCredentials,
  } = useAuth();

  const [activeMode, setActiveMode] = useState<"extract" | "synthesis" | "quick_log">("extract");
  const [projectsList, setProjectsList] = useState<string[]>([]);
  const [project, setProject] = useState("default");
  const [rawText, setRawText] = useState("");
  const [isExtracting, setIsExtracting] = useState(false);
  const [isSaving, setIsSaving] = useState(false);

  // Editable Draft Items
  const [drafts, setDrafts] = useState<DraftItem[]>([]);
  const [extractedEdges, setExtractedEdges] = useState<ExtractedEdge[]>([]);
  const [extractionSummary, setExtractionSummary] = useState<string>("");
  const [sourceMethod, setSourceMethod] = useState<string>("");

  // Synthesis State
  const [synthesisResult, setSynthesisResult] = useState<SynthesisResult | null>(null);
  const [isSynthesizing, setIsSynthesizing] = useState(false);
  const [copiedSynthesis, setCopiedSynthesis] = useState(false);

  // Quick Log State
  const [quickTitle, setQuickTitle] = useState("");
  const [quickContent, setQuickContent] = useState("");
  const [quickType, setQuickType] = useState("decision");
  const [quickTags, setQuickTags] = useState("");
  const [isQuickSaving, setIsQuickSaving] = useState(false);

  // LLM Config
  const [showLLMSettings, setShowLLMSettings] = useState(false);
  const [apiKeyInput, setApiKeyInput] = useState(llmApiKey);
  const [providerInput, setProviderInput] = useState(llmProvider || "openai");
  const [modelInput, setModelInput] = useState(llmModel || "gpt-4o-mini");
  const [baseURLInput, setBaseURLInput] = useState(llmBaseURL || "");
  const [saveSuccessMessage, setSaveSuccessMessage] = useState<string | null>(null);

  useEffect(() => {
    loadProjects();
  }, [client]);

  const loadProjects = async () => {
    if (!client) return;
    try {
      const p = await client.projects();
      if (p && p.length > 0) {
        setProjectsList(p);
        if (!p.includes(project)) {
          setProject(p[0]);
        }
      }
    } catch {
      setProjectsList(["default", "cortex-core", "api-service"]);
    }
  };

  const handleSaveLLMSettings = (e: React.FormEvent) => {
    e.preventDefault();
    setLLMCredentials(apiKeyInput, providerInput, modelInput, baseURLInput);
    setShowLLMSettings(false);
  };

  const handleApplyTemplate = (tplKey: keyof typeof TEMPLATES) => {
    setRawText(TEMPLATES[tplKey].text);
  };

  const handleFileUpload = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = (event) => {
      const content = event.target?.result as string;
      if (content) {
        setRawText(content);
      }
    };
    reader.readAsText(file);
  };

  const handleExtract = async () => {
    if (!client || !rawText.trim()) return;
    setIsExtracting(true);
    setDrafts([]);
    setExtractedEdges([]);
    setExtractionSummary("");
    setSaveSuccessMessage(null);

    try {
      const res = await client.extract({
        text: rawText,
        project: project,
        llm_config:
          apiKeyInput || baseURLInput || providerInput === "ollama"
            ? {
                provider: providerInput,
                api_key: apiKeyInput,
                model: modelInput,
                base_url: baseURLInput || undefined,
              }
            : undefined,
      });

      const items: DraftItem[] = (res.observations || []).map((o) => ({
        ...o,
        selected: true,
      }));
      setDrafts(items);
      setExtractedEdges(res.edges || []);
      setExtractionSummary(res.summary || "Observaciones y entidades extraídas con éxito.");
      setSourceMethod(res.source_method || "llm");
    } catch (err: any) {
      alert("Error en la extracción: " + (err.message || err));
    } finally {
      setIsExtracting(false);
    }
  };

  const toggleSelectAllDrafts = () => {
    const allSelected = drafts.every((d) => d.selected);
    setDrafts(drafts.map((d) => ({ ...d, selected: !allSelected })));
  };

  const updateDraft = (index: number, field: keyof DraftItem, value: any) => {
    setDrafts((prev) => {
      const copy = [...prev];
      copy[index] = { ...copy[index], [field]: value };
      return copy;
    });
  };

  const handleSaveSelectedToCortex = async () => {
    if (!client) return;
    const selectedItems = drafts.filter((d) => d.selected);
    if (selectedItems.length === 0) {
      alert("Por favor selecciona al menos una observación para guardar.");
      return;
    }

    setIsSaving(true);
    try {
      const savedObs: any[] = [];
      for (const draft of selectedItems) {
        const o = await client.createObservation({
          title: draft.title,
          content: draft.content,
          type: draft.type,
          project: draft.project || project,
          scope: draft.scope || "project",
          tags: draft.tags || [],
          confidence: draft.confidence || 0.9,
        });
        savedObs.push(o);
      }

      // Create graph edges for saved items
      let edgesCreated = 0;
      for (const edge of extractedEdges) {
        const fromObs = savedObs.find((o) => o.title === edge.from_title);
        const toObs = savedObs.find((o) => o.title === edge.to_title);
        if (fromObs && toObs && fromObs.id && toObs.id) {
          try {
            await client.createEdge({
              from_id: fromObs.id,
              to_id: toObs.id,
              relation_type: edge.relation_type || "relates_to",
              confidence: edge.confidence || 0.8,
              reasoning: edge.reasoning,
            });
            edgesCreated++;
          } catch {
            // Edge might already exist or fail softly
          }
        }
      }

      setSaveSuccessMessage(
        `¡Se persistieron ${savedObs.length} observaciones y ${edgesCreated} relaciones en el grafo de Cortex!`,
      );
      setDrafts([]);
      setExtractedEdges([]);
      setRawText("");
    } catch (err: any) {
      alert("Error al guardar en Cortex: " + (err.message || err));
    } finally {
      setIsSaving(false);
    }
  };

  const handleSynthesize = async () => {
    if (!client) return;
    setIsSynthesizing(true);
    setSynthesisResult(null);
    try {
      const obsList = await client.listObservations(
        `?project=${encodeURIComponent(project)}&limit=30`,
      );
      if (!obsList || obsList.length === 0) {
        alert(
          `No se encontraron observaciones en el proyecto "${project}". Agrega observaciones primero para generar una síntesis.`,
        );
        return;
      }
      const res = await client.synthesize({
        project,
        observations: obsList,
        llm_config:
          apiKeyInput || baseURLInput || providerInput === "ollama"
            ? {
                provider: providerInput,
                api_key: apiKeyInput,
                model: modelInput,
                base_url: baseURLInput || undefined,
              }
            : undefined,
      });
      setSynthesisResult(res);
    } catch (err: any) {
      alert("Error al sintetizar el proyecto: " + (err.message || err));
    } finally {
      setIsSynthesizing(false);
    }
  };

  const copySynthesisMarkdown = () => {
    if (!synthesisResult) return;
    const md = `# Síntesis Técnica del Proyecto: ${synthesisResult.project}
Generado: ${new Date(synthesisResult.synthesized_at).toLocaleString()}

## Resumen Ejecutivo
${synthesisResult.summary}

## Decisiones Clave de Arquitectura
${synthesisResult.key_decisions.map((d) => `- ${d}`).join("\n")}

## Patrones y Estándares de Código
${synthesisResult.patterns.map((p) => `- ${p}`).join("\n")}

## Problemas Abiertos y Deuda Técnica
${synthesisResult.open_issues.map((i) => `- ${i}`).join("\n")}
`;
    navigator.clipboard.writeText(md);
    setCopiedSynthesis(true);
    setTimeout(() => setCopiedSynthesis(false), 2500);
  };

  const handleQuickSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !quickTitle.trim() || !quickContent.trim()) return;
    setIsQuickSaving(true);
    try {
      const tagsArray = quickTags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean);

      await client.createObservation({
        title: quickTitle,
        content: quickContent,
        type: quickType,
        project: project,
        scope: "project",
        tags: tagsArray,
        confidence: 1.0,
      });

      setSaveSuccessMessage(`¡Observación "${quickTitle}" registrada con éxito en ${project}!`);
      setQuickTitle("");
      setQuickContent("");
      setQuickTags("");
    } catch (err: any) {
      alert("Error al guardar: " + (err.message || err));
    } finally {
      setIsQuickSaving(false);
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
    } else if (p === "gemini") {
      setBaseURLInput("https://generativelanguage.googleapis.com/v1beta/openai");
      setModelInput("gemini-2.5-flash");
    } else if (p === "deepseek") {
      setBaseURLInput("https://api.deepseek.com/v1");
      setModelInput("deepseek-chat");
    }
  };

  return (
    <div className="space-y-6">
      {/* Top Banner: Explanatory & Utility Header */}
      <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-4 p-5 rounded-2xl bg-gradient-to-r from-blue-600/10 via-purple-600/10 to-transparent border border-blue-500/20 shadow-xl">
        <div className="space-y-1.5">
          <div className="flex items-center gap-2">
            <Sparkles className="h-5 w-5 text-blue-400 shrink-0" />
            <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text-primary)]">
              Centro de Extracción, Síntesis & Captura Inteligente con IA
            </h1>
          </div>
          <p className="text-xs text-[var(--text-secondary)] max-w-3xl leading-relaxed">
            Convierte transcripciones desestructuradas de terminal, chats de IA (Claude Code, Cursor), incidentes de producción o logs de commits en <b>observaciones atómicas indexables</b> y <b>relaciones de grafo</b> para alimentar la memoria compartida del equipo.
          </p>
        </div>

        <div className="flex flex-wrap items-center gap-2 shrink-0">
          <Button
            onClick={() => setShowLLMSettings(!showLLMSettings)}
            variant="outline"
            size="sm"
            className="gap-2 text-xs border-[var(--border-subtle)] bg-[var(--bg-surface)]"
          >
            <Settings className="h-4 w-4 text-blue-400" />
            <span>{apiKeyInput || baseURLInput ? "IA Personalizada: Activa" : "Configurar LLM"}</span>
          </Button>
        </div>
      </div>

      {/* LLM Settings Collapsible Card */}
      {showLLMSettings && (
        <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl animate-in fade-in">
          <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)] mb-4">
            <CardTitle className="text-sm text-[var(--text-primary)] flex items-center gap-2">
              <Key className="h-4 w-4 text-blue-400" />
              <span>Configuración de Proveedor LLM Personalizado</span>
            </CardTitle>
            <Badge variant="outline" className="text-[10px] font-mono">
              In-Memory Client Credential
            </Badge>
          </div>

          <form onSubmit={handleSaveLLMSettings} className="space-y-3 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PROVEEDOR
                </label>
                <Select
                  value={providerInput}
                  onChange={(e) => handleProviderSelect(e.target.value)}
                  className="h-9 text-xs w-full"
                >
                  <option value="openai">OpenAI</option>
                  <option value="gemini">Google Gemini</option>
                  <option value="anthropic">Anthropic Claude</option>
                  <option value="ollama">Ollama (Local)</option>
                  <option value="openrouter">OpenRouter</option>
                  <option value="groq">Groq LPU</option>
                  <option value="together">Together AI</option>
                  <option value="deepseek">DeepSeek</option>
                  <option value="custom">Personalizado</option>
                </Select>
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  MODELO
                </label>
                <Input
                  type="text"
                  placeholder="gpt-4o-mini / llama3.3"
                  value={modelInput}
                  onChange={(e) => setModelInput(e.target.value)}
                  className="h-9 text-xs font-mono w-full"
                  required
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  API ENDPOINT / BASE URL
                </label>
                <Input
                  type="text"
                  placeholder="https://api.openai.com/v1"
                  value={baseURLInput}
                  onChange={(e) => setBaseURLInput(e.target.value)}
                  className="h-9 text-xs font-mono w-full"
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  API KEY / TOKEN
                </label>
                <Input
                  type="password"
                  placeholder={providerInput === "ollama" ? "Opcional (Ollama)" : "sk-..."}
                  value={apiKeyInput}
                  onChange={(e) => setApiKeyInput(e.target.value)}
                  className="h-9 text-xs font-mono w-full"
                />
              </div>
            </div>

            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 pt-2 border-t border-[var(--border-subtle)]">
              <span className="text-[11px] text-[var(--text-muted)]">
                Las credenciales se guardan localmente en el navegador para tus peticiones de IA.
              </span>
              <Button type="submit" size="sm" className="h-8 text-xs shrink-0">
                Guardar Configuración
              </Button>
            </div>
          </form>
        </Card>
      )}

      {/* Success Notification */}
      {saveSuccessMessage && (
        <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 p-4 rounded-xl text-xs flex items-center justify-between gap-2.5">
          <div className="flex items-center gap-2.5">
            <CheckCircle className="h-5 w-5 shrink-0" />
            <span>{saveSuccessMessage}</span>
          </div>
          <div className="flex items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => router.push("/memory")}
              className="h-7 text-[11px] text-emerald-300 border-emerald-500/30"
            >
              Ver en Memoria
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => router.push(`/graph?project=${encodeURIComponent(project)}`)}
              className="h-7 text-[11px] text-emerald-300 border-emerald-500/30"
            >
              Ver en Grafo
            </Button>
          </div>
        </div>
      )}

      {/* Mode Navigation Tabs */}
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-[var(--border-subtle)] pb-3">
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => setActiveMode("extract")}
            className={`px-4 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
              activeMode === "extract"
                ? "bg-blue-600 text-white shadow-lg shadow-blue-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <Sparkles className="h-4 w-4" />
            <span>1. Extractor de Transcripciones & Notas</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveMode("synthesis")}
            className={`px-4 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
              activeMode === "synthesis"
                ? "bg-purple-600 text-white shadow-lg shadow-purple-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <Layers className="h-4 w-4" />
            <span>2. Síntesis y Reporte de Proyecto</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveMode("quick_log")}
            className={`px-4 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
              activeMode === "quick_log"
                ? "bg-emerald-600 text-white shadow-lg shadow-emerald-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <BookOpen className="h-4 w-4" />
            <span>3. Captura Rápida Guiada</span>
          </button>
        </div>

        {/* Global Project Context Selector */}
        <div className="flex items-center gap-2 bg-[var(--bg-surface)] border border-[var(--border-subtle)] px-3 py-1 rounded-xl shadow-sm">
          <FolderKanban className="h-4 w-4 text-blue-400 shrink-0" />
          <span className="text-[11px] text-[var(--text-muted)] font-medium">Proyecto:</span>
          <Select
            value={project}
            onChange={(e) => setProject(e.target.value)}
            className="bg-transparent border-0 font-semibold text-xs text-[var(--text-primary)] focus:ring-0 cursor-pointer min-w-[140px]"
          >
            {projectsList.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </Select>
        </div>
      </div>

      {/* MODE 1: EXTRACTOR */}
      {activeMode === "extract" && (
        <div className="space-y-6">
          <Card className="p-5 sm:p-6 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
              <div>
                <h3 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                  <FileText className="h-4 w-4 text-blue-400" />
                  <span>Ingesta de Texto Desestructurado</span>
                </h3>
                <p className="text-xs text-[var(--text-muted)] mt-0.5">
                  Pega texto libre o selecciona una plantilla de ejemplo para probar la extracción automática.
                </p>
              </div>

              {/* Template Quick Actions */}
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-[11px] text-[var(--text-muted)] font-medium">Plantillas:</span>
                <button
                  type="button"
                  onClick={() => handleApplyTemplate("bugfix")}
                  className="px-2.5 py-1 rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-surface-hover)] border border-[var(--border-subtle)] text-[11px] text-red-400 font-medium transition-all"
                >
                  🐛 Bugfix
                </button>
                <button
                  type="button"
                  onClick={() => handleApplyTemplate("architecture")}
                  className="px-2.5 py-1 rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-surface-hover)] border border-[var(--border-subtle)] text-[11px] text-blue-400 font-medium transition-all"
                >
                  🏗️ Arquitectura
                </button>
                <button
                  type="button"
                  onClick={() => handleApplyTemplate("pr_log")}
                  className="px-2.5 py-1 rounded-lg bg-[var(--bg-surface)] hover:bg-[var(--bg-surface-hover)] border border-[var(--border-subtle)] text-[11px] text-purple-400 font-medium transition-all"
                >
                  🚀 Pull Request
                </button>
              </div>
            </div>

            <textarea
              className="flex min-h-[160px] w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-3.5 text-xs text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500 font-mono leading-relaxed"
              rows={7}
              placeholder="Pega aquí transcripciones de chats de Claude/Cursor, explicaciones técnicas de incidentes, notas de reunión o código..."
              value={rawText}
              onChange={(e) => setRawText(e.target.value)}
            />

            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pt-2">
              <label className="flex items-center gap-2 text-xs text-[var(--text-muted)] cursor-pointer hover:text-[var(--text-primary)]">
                <Upload className="h-4 w-4 text-blue-400" />
                <span>Cargar archivo .md / .txt</span>
                <input
                  type="file"
                  accept=".txt,.md,.json,.log"
                  onChange={handleFileUpload}
                  className="hidden"
                />
              </label>

              <div className="flex items-center gap-2">
                {rawText && (
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setRawText("")}
                    className="text-xs text-[var(--text-muted)]"
                  >
                    Limpiar
                  </Button>
                )}
                <Button
                  onClick={handleExtract}
                  size="sm"
                  disabled={isExtracting || !rawText.trim()}
                  className="gap-2 shadow-lg shadow-blue-600/20 text-xs bg-blue-600 hover:bg-blue-500 text-white px-5"
                >
                  <Sparkles className="h-4 w-4" />
                  <span>{isExtracting ? "Analizando y Extrayendo..." : "Ejecutar Extracción IA"}</span>
                </Button>
              </div>
            </div>
          </Card>

          {/* Extracted Drafts Reviewer */}
          {drafts.length > 0 && (
            <div className="space-y-4 animate-in fade-in">
              <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                <div className="space-y-0.5">
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-bold text-[var(--text-primary)]">
                      Revisión de Observaciones Extraídas ({drafts.length})
                    </h3>
                    <Badge variant="outline" className="text-[10px] uppercase font-mono">
                      {sourceMethod}
                    </Badge>
                  </div>
                  <p className="text-xs text-[var(--text-muted)]">{extractionSummary}</p>
                </div>

                <div className="flex items-center gap-2 shrink-0">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={toggleSelectAllDrafts}
                    className="text-xs border-[var(--border-subtle)]"
                  >
                    {drafts.every((d) => d.selected) ? "Deseleccionar Todos" : "Seleccionar Todos"}
                  </Button>
                  <Button
                    onClick={handleSaveSelectedToCortex}
                    size="sm"
                    disabled={isSaving || drafts.filter((d) => d.selected).length === 0}
                    className="gap-2 shadow-lg shadow-emerald-600/20 text-xs bg-emerald-600 hover:bg-emerald-500 text-white"
                  >
                    <Database className="h-4 w-4" />
                    <span>
                      {isSaving
                        ? "Guardando..."
                        : `Persistir (${drafts.filter((d) => d.selected).length}) en Cortex`}
                    </span>
                  </Button>
                </div>
              </div>

              {/* Observation Cards Grid */}
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                {drafts.map((draft, idx) => (
                  <Card
                    key={idx}
                    className={`p-4 bg-[var(--bg-secondary)] border transition-all space-y-3 ${
                      draft.selected
                        ? "border-blue-500/50 shadow-md shadow-blue-500/5"
                        : "border-[var(--border-subtle)] opacity-60"
                    }`}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2 flex-1">
                        <button
                          type="button"
                          onClick={() => updateDraft(idx, "selected", !draft.selected)}
                          className="text-blue-400 hover:text-blue-300"
                        >
                          {draft.selected ? (
                            <CheckSquare className="h-4 w-4" />
                          ) : (
                            <Square className="h-4 w-4 text-[var(--text-muted)]" />
                          )}
                        </button>
                        <Input
                          value={draft.title}
                          onChange={(e) => updateDraft(idx, "title", e.target.value)}
                          className="h-7 text-xs font-semibold bg-transparent border-0 focus:border p-1"
                        />
                      </div>

                      <Select
                        value={draft.type}
                        onChange={(e) => updateDraft(idx, "type", e.target.value)}
                        className="h-6 text-[10px] w-24 bg-[var(--bg-surface)]"
                      >
                        <option value="decision">decision</option>
                        <option value="bugfix">bugfix</option>
                        <option value="pattern">pattern</option>
                        <option value="discovery">discovery</option>
                        <option value="config">config</option>
                      </Select>
                    </div>

                    <textarea
                      rows={3}
                      value={draft.content}
                      onChange={(e) => updateDraft(idx, "content", e.target.value)}
                      className="w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-2 text-xs text-[var(--text-secondary)] focus:outline-none focus:ring-1 focus:ring-blue-500 leading-relaxed font-mono"
                    />

                    {draft.tags && draft.tags.length > 0 && (
                      <div className="flex flex-wrap gap-1 pt-1">
                        {draft.tags.map((tag, tIdx) => (
                          <span
                            key={tIdx}
                            className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-[var(--text-muted)] font-mono"
                          >
                            #{tag}
                          </span>
                        ))}
                      </div>
                    )}
                  </Card>
                ))}
              </div>

              {/* Detected Edges */}
              {extractedEdges.length > 0 && (
                <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-3 shadow-lg">
                  <CardTitle className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider flex items-center gap-2">
                    <Share2 className="h-4 w-4 text-blue-400" />
                    <span>Relaciones Detectadas para el Grafo ({extractedEdges.length})</span>
                  </CardTitle>
                  <div className="grid grid-cols-1 md:grid-cols-2 gap-2.5">
                    {extractedEdges.map((e, idx) => (
                      <div
                        key={idx}
                        className="p-3 bg-[var(--bg-surface)] border border-[var(--border-subtle)] rounded-xl flex flex-col justify-between gap-1 text-xs"
                      >
                        <div className="flex flex-wrap items-center gap-1.5 font-medium">
                          <span className="text-[var(--text-primary)]">{e.from_title}</span>
                          <span className="text-[var(--text-muted)]">➔</span>
                          <Badge variant="outline" className="text-[10px] font-mono text-blue-400">
                            {e.relation_type}
                          </Badge>
                          <span className="text-[var(--text-muted)]">➔</span>
                          <span className="text-[var(--text-primary)]">{e.to_title}</span>
                        </div>
                        {e.reasoning && (
                          <span className="text-[11px] text-[var(--text-muted)] italic">
                            {e.reasoning}
                          </span>
                        )}
                      </div>
                    ))}
                  </div>
                </Card>
              )}
            </div>
          )}
        </div>
      )}

      {/* MODE 2: SYNTHESIS */}
      {activeMode === "synthesis" && (
        <div className="space-y-6">
          <Card className="p-5 sm:p-6 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
              <div>
                <h3 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                  <Layers className="h-4 w-4 text-purple-400" />
                  <span>Generador de Ficha y Síntesis de Arquitectura</span>
                </h3>
                <p className="text-xs text-[var(--text-muted)] mt-0.5">
                  El LLM analiza todo el historial de observaciones del proyecto <b>{project}</b> para sintetizar el estado del software.
                </p>
              </div>

              <Button
                onClick={handleSynthesize}
                disabled={isSynthesizing}
                size="sm"
                className="gap-2 bg-purple-600 hover:bg-purple-500 text-white text-xs shadow-lg shadow-purple-600/20 px-5 shrink-0"
              >
                <Sparkles className="h-4 w-4" />
                <span>{isSynthesizing ? "Sintetizando Proyecto..." : "Generar Síntesis"}</span>
              </Button>
            </div>

            {synthesisResult ? (
              <div className="space-y-6 animate-in fade-in pt-2">
                <div className="flex items-center justify-between p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                  <div>
                    <h4 className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider">
                      Resumen Ejecutivo del Proyecto
                    </h4>
                    <p className="text-xs text-[var(--text-secondary)] mt-1 leading-relaxed">
                      {synthesisResult.summary}
                    </p>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={copySynthesisMarkdown}
                    className="text-xs gap-1.5 shrink-0 border-[var(--border-subtle)] ml-3"
                  >
                    {copiedSynthesis ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                    <span>{copiedSynthesis ? "¡Copiado!" : "Copiar Markdown"}</span>
                  </Button>
                </div>

                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                  {/* Key Decisions */}
                  <Card className="p-4 bg-[var(--bg-surface)] border-blue-500/30 space-y-2.5">
                    <div className="flex items-center gap-2 pb-2 border-b border-[var(--border-subtle)]">
                      <Cpu className="h-4 w-4 text-blue-400" />
                      <h4 className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider">
                        Decisiones Clave ({synthesisResult.key_decisions.length})
                      </h4>
                    </div>
                    <ul className="space-y-2 text-xs text-[var(--text-secondary)]">
                      {synthesisResult.key_decisions.map((d, i) => (
                        <li key={i} className="flex items-start gap-2">
                          <span className="text-blue-400 font-bold">•</span>
                          <span className="leading-relaxed">{d}</span>
                        </li>
                      ))}
                    </ul>
                  </Card>

                  {/* Patterns */}
                  <Card className="p-4 bg-[var(--bg-surface)] border-purple-500/30 space-y-2.5">
                    <div className="flex items-center gap-2 pb-2 border-b border-[var(--border-subtle)]">
                      <Code2 className="h-4 w-4 text-purple-400" />
                      <h4 className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider">
                        Patrones de Código ({synthesisResult.patterns.length})
                      </h4>
                    </div>
                    <ul className="space-y-2 text-xs text-[var(--text-secondary)]">
                      {synthesisResult.patterns.map((p, i) => (
                        <li key={i} className="flex items-start gap-2">
                          <span className="text-purple-400 font-bold">•</span>
                          <span className="leading-relaxed">{p}</span>
                        </li>
                      ))}
                    </ul>
                  </Card>

                  {/* Open Issues */}
                  <Card className="p-4 bg-[var(--bg-surface)] border-amber-500/30 space-y-2.5">
                    <div className="flex items-center gap-2 pb-2 border-b border-[var(--border-subtle)]">
                      <Bug className="h-4 w-4 text-amber-400" />
                      <h4 className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider">
                        Problemas / Deuda ({synthesisResult.open_issues.length})
                      </h4>
                    </div>
                    <ul className="space-y-2 text-xs text-[var(--text-secondary)]">
                      {synthesisResult.open_issues.map((issue, i) => (
                        <li key={i} className="flex items-start gap-2">
                          <span className="text-amber-400 font-bold">•</span>
                          <span className="leading-relaxed">{issue}</span>
                        </li>
                      ))}
                    </ul>
                  </Card>
                </div>
              </div>
            ) : (
              <div className="py-12 text-center text-[var(--text-muted)] space-y-3 bg-[var(--bg-surface)] rounded-xl border border-[var(--border-subtle)]">
                <Layers className="h-8 w-8 mx-auto text-[var(--text-muted)] opacity-50" />
                <div className="text-xs">
                  Haz clic en <b>"Generar Síntesis"</b> para que la IA elabore el informe consolidado de <b>{project}</b>.
                </div>
              </div>
            )}
          </Card>
        </div>
      )}

      {/* MODE 3: QUICK GUIDED CAPTURE */}
      {activeMode === "quick_log" && (
        <Card className="p-5 sm:p-7 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl space-y-4">
          <div className="pb-3 border-b border-[var(--border-subtle)]">
            <h3 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
              <BookOpen className="h-4 w-4 text-emerald-400" />
              <span>Captura Rápida de Conocimiento (Quick Log)</span>
            </h3>
            <p className="text-xs text-[var(--text-muted)] mt-0.5">
              Registra directamente una decisión o solución arquitectónica en el proyecto <b>{project}</b> sin pasar por transcripciones.
            </p>
          </div>

          <form onSubmit={handleQuickSave} className="space-y-4 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="sm:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  TÍTULO CONCISO *
                </label>
                <Input
                  required
                  placeholder="ej: Migración a PostgreSQL 16 con RLS estricto"
                  value={quickTitle}
                  onChange={(e) => setQuickTitle(e.target.value)}
                  className="h-9 text-xs"
                />
              </div>

              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  TIPO DE OBSERVACIÓN
                </label>
                <Select
                  value={quickType}
                  onChange={(e) => setQuickType(e.target.value)}
                  className="h-9 text-xs w-full"
                >
                  <option value="decision">decision (Decisión de diseño)</option>
                  <option value="bugfix">bugfix (Solución de bug)</option>
                  <option value="pattern">pattern (Patrón estándar)</option>
                  <option value="discovery">discovery (Hallazgo técnico)</option>
                  <option value="config">config (Regla de configuración)</option>
                </Select>
              </div>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                CONTENIDO / EXPLICACIÓN TÉCNICA *
              </label>
              <textarea
                required
                rows={4}
                placeholder="Describe la decisión tomada, contexto, causas o reglas a respetar por los agentes..."
                value={quickContent}
                onChange={(e) => setQuickContent(e.target.value)}
                className="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-3 text-xs text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:ring-1 focus:ring-emerald-500 font-mono leading-relaxed"
              />
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                TAGS (SEPARADOS POR COMA)
              </label>
              <Input
                placeholder="postgres, authz, zero-cgo, performance"
                value={quickTags}
                onChange={(e) => setQuickTags(e.target.value)}
                className="h-9 text-xs font-mono"
              />
            </div>

            <div className="flex justify-end pt-2">
              <Button
                type="submit"
                disabled={isQuickSaving}
                className="gap-2 bg-emerald-600 hover:bg-emerald-500 text-white text-xs px-6 shadow-md shadow-emerald-600/20"
              >
                <CheckCircle className="h-4 w-4" />
                <span>{isQuickSaving ? "Guardando..." : "Guardar Observación"}</span>
              </Button>
            </div>
          </form>
        </Card>
      )}
    </div>
  );
}

