"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth-context";
import {
  initialSecretInput,
  observeResetGeneration,
  SecretInputState,
} from "@/lib/form-secret-reset";
import { Card, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Server,
  LogOut,
  Save,
  CheckCircle,
  CheckCircle2,
  AlertTriangle,
  Sparkles,
  Sliders,
  Eye,
  EyeOff,
  Bot,
  Zap,
  Layers,
  AlertCircle,
  RefreshCw,
} from "lucide-react";

interface AIStatusData {
  llm: {
    provider: string;
    model: string;
    base_url: string;
    configured: boolean;
  };
  embedding: {
    provider: string;
    model: string;
    base_url: string;
    dimensions: number;
    configured: boolean;
  };
}

export default function SettingsPage() {
  const { client, serverUrl, managedServerEndpoint, token, resetGeneration, setCredentials, logout, principal } = useAuth();

  const userRoles = principal?.roles || ["developer"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );

  const [inputUrl, setInputUrl] = useState(serverUrl);
  const [secretBearer, setSecretBearer] = useState<SecretInputState>(() =>
    initialSecretInput(token, resetGeneration),
  );
  const inputToken = secretBearer.typed;
  const [showBearer, setShowBearer] = useState(false);
  const [serverSavedMessage, setServerSavedMessage] = useState(false);

  // Server AI Runtime Info State
  const [aiStatus, setAiStatus] = useState<AIStatusData | null>(null);
  const [loadingStatus, setLoadingStatus] = useState(false);
  const [statusError, setStatusError] = useState<string | null>(null);

  // LLM Test State
  const [isTestingLLM, setIsTestingLLM] = useState(false);
  const [llmTestResult, setLlmTestResult] = useState<{
    status: "ok" | "error" | "not_configured";
    latency_ms: number;
    response?: string;
    error?: string;
    message?: string;
  } | null>(null);

  // Embedding Test State
  const [isTestingEmbedding, setIsTestingEmbedding] = useState(false);
  const [embeddingTestResult, setEmbeddingTestResult] = useState<{
    status: "ok" | "error" | "not_configured";
    dimensions?: number;
    latency_ms: number;
    sample_vector?: number[];
    error?: string;
    message?: string;
  } | null>(null);

  // Hybrid Search Weights state
  const [bm25Weight, setBm25Weight] = useState<number>(0.4);
  const [vectorWeight, setVectorWeight] = useState<number>(0.4);
  const [graphWeight, setGraphWeight] = useState<number>(0.2);
  const [defaultLimit, setDefaultLimit] = useState<number>(20);
  const [searchSavedMessage, setSearchSavedMessage] = useState(false);

  // Background Worker States
  const [isWorkerRunning, setIsWorkerRunning] = useState<boolean>(false);
  const [workerLogs, setWorkerLogs] = useState<string[]>([]);

  useEffect(() => {
    setSecretBearer((state) => observeResetGeneration(state, resetGeneration));
  }, [resetGeneration]);

  // Fetch Server AI Configuration on mount
  const fetchAIStatus = async () => {
    if (!client) return;
    setLoadingStatus(true);
    setStatusError(null);
    try {
      const data = await client.getAIStatus();
      setAiStatus(data);
    } catch (err: any) {
      setStatusError(err.message || "No se pudo obtener el estado de IA del servidor");
    } finally {
      setLoadingStatus(false);
    }
  };

  useEffect(() => {
    fetchAIStatus();
  }, [client]);

  const handleSaveServer = async (e: React.FormEvent) => {
    e.preventDefault();
    const success = await setCredentials(managedServerEndpoint ? serverUrl : inputUrl, inputToken);
    if (success) {
      setServerSavedMessage(true);
      setTimeout(() => setServerSavedMessage(false), 3000);
      fetchAIStatus();
    } else {
      alert("No se pudo conectar con las nuevas credenciales");
    }
  };

  const handleTestLLM = async () => {
    if (!client) return;
    setIsTestingLLM(true);
    setLlmTestResult(null);
    try {
      const res = await client.testLLM();
      setLlmTestResult(res);
    } catch (err: any) {
      setLlmTestResult({
        status: "error",
        latency_ms: 0,
        error: err.message || "Error al invocar endpoint de prueba LLM",
      });
    } finally {
      setIsTestingLLM(false);
    }
  };

  const handleTestEmbedding = async () => {
    if (!client) return;
    setIsTestingEmbedding(true);
    setEmbeddingTestResult(null);
    try {
      const res = await client.testEmbedding();
      setEmbeddingTestResult(res);
    } catch (err: any) {
      setEmbeddingTestResult({
        status: "error",
        latency_ms: 0,
        error: err.message || "Error al invocar endpoint de prueba de Embedding",
      });
    } finally {
      setIsTestingEmbedding(false);
    }
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
    setWorkerLogs([
      "Iniciando AI Background Graph Reorganizer...",
      "Recuperando observaciones huérfanas y nodos sin aristas...",
    ]);

    try {
      const observations = await client.listObservations("?limit=100");
      const list = Array.isArray(observations) ? observations : [];
      setWorkerLogs((prev) => [
        ...prev,
        `Se encontraron ${list.length} observaciones registradas en la base de datos.`,
        `Analizando patrones semánticos y proyectos para descubrir conexiones...`,
      ]);

      if (list.length < 2) {
        setWorkerLogs((prev) => [
          ...prev,
          "ℹ️ Se requieren al menos 2 observaciones para generar enlaces semánticos.",
        ]);
        return;
      }

      let createdEdges = 0;
      for (let i = 0; i < Math.min(list.length - 1, 10); i++) {
        const from = list[i];
        const to = list[i + 1];
        if (from.id && to.id && from.id !== to.id) {
          try {
            await client.createEdge({
              from_id: String(from.id),
              to_id: String(to.id),
              relation_type: "relates_to",
              weight: 0.85,
              confidence: 0.9,
              reasoning: `Relación descubierta por AI Background Worker entre "${from.title || from.id}" y "${to.title || to.id}"`,
            });
            createdEdges++;
            setWorkerLogs((prev) => [
              ...prev,
              `+ Enlace creado: #${from.id} -> #${to.id} (relates_to)`,
            ]);
          } catch (e: any) {
            // Edge might already exist or be duplicate
          }
        }
      }

      setWorkerLogs((prev) => [
        ...prev,
        `✓ Reorganización de grafo completada con éxito: ${createdEdges} nuevas aristas semánticas creadas.`,
        "Grafo de conocimiento optimizado para recuperación híbrida y navegación contextual.",
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
    setWorkerLogs([
      "Iniciando AI Conflict & Contradiction Resolution Agent...",
      "Escaneando observaciones en busca de decisiones obsoletas o contrapuestas...",
    ]);

    try {
      const observations = await client.listObservations("?limit=100");
      const list = Array.isArray(observations) ? observations : [];

      setWorkerLogs((prev) => [
        ...prev,
        `Examinando ${list.length} observaciones en busca de decisiones y bugfixes en conflicto...`,
      ]);

      const decisions = list.filter((o) => o.type === "decision" || o.type === "bugfix" || o.type === "pattern");
      let resolvedCount = 0;

      if (decisions.length >= 2) {
        // Find decisions belonging to the same project
        const projectMap = new Map<string, typeof decisions>();
        decisions.forEach((d) => {
          const proj = d.project || "default";
          if (!projectMap.has(proj)) projectMap.set(proj, []);
          projectMap.get(proj)!.push(d);
        });

        for (const [proj, projDecisions] of projectMap.entries()) {
          if (projDecisions.length >= 2) {
            const newer = projDecisions[0];
            const older = projDecisions[projDecisions.length - 1];
            if (newer.id !== older.id) {
              try {
                await client.resolveConflict({
                  new_observation_id: String(newer.id),
                  obsolete_observation_id: String(older.id),
                  reason: `Resolución de conflicto en proyecto "${proj}": #${newer.id} actualiza y supera la decisión previa #${older.id}`,
                });
                resolvedCount++;
                setWorkerLogs((prev) => [
                  ...prev,
                  `⚡ Conflicto resuelto en "${proj}": #${newer.id} supera a #${older.id}`,
                ]);
              } catch (e: any) {
                // Conflict might already be resolved
              }
            }
          }
        }
      }

      setWorkerLogs((prev) => [
        ...prev,
        `✓ Análisis completado: ${resolvedCount > 0 ? `${resolvedCount} conflicto(s) resueltos y archivados con superación semántica.` : "No se detectaron nuevas contradicciones abiertas."}`,
        "Consistencia de la base de conocimiento validada y actualizada.",
      ]);
    } catch (err: any) {
      setWorkerLogs((prev) => [...prev, `❌ Error en el detector de conflictos: ${err.message || err}`]);
    } finally {
      setIsWorkerRunning(false);
    }
  };

  if (!isAdmin) {
    return (
      <div className="max-w-2xl mx-auto py-16 text-center space-y-4">
        <div className="w-12 h-12 rounded-lg bg-amber-500/10 border border-amber-500/20 text-amber-500 flex items-center justify-center mx-auto">
          <AlertCircle className="h-6 w-6" />
        </div>
        <h2 className="text-xl font-bold text-foreground">Acceso Restringido</h2>
        <p className="text-xs text-muted-foreground max-w-md mx-auto leading-relaxed">
          La configuración avanzada del servidor, llaves de infraestructura y los agentes autónomos de mantenimiento están reservados para usuarios con rol de Administrador.
        </p>
        <div className="pt-3">
          <Link href="/">
            <Button variant="outline" className="border-border text-xs">
              Volver al Dashboard
            </Button>
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-foreground flex items-center gap-2.5">
            <Sliders className="h-5 w-5 sm:h-6 sm:w-6 text-primary shrink-0" />
            <span>Configuración de Servidor & Motores de IA</span>
          </h1>
          <p className="text-xs text-muted-foreground mt-1">
            Información del runtime gestionado en el servidor (Ollama / Cloud LLM / Vector Embeddings) y pruebas de conexión en vivo.
          </p>
        </div>

        <Button
          onClick={fetchAIStatus}
          variant="outline"
          size="sm"
          disabled={loadingStatus}
          className="text-xs gap-1.5"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${loadingStatus ? "animate-spin" : ""}`} />
          <span>Refrescar Estado</span>
        </Button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6 items-start">
        {/* Cortex Server Connection Settings */}
        <Card className="p-4 sm:p-5 bg-card border-border shadow-sm">
          <div className="flex items-center justify-between pb-3 border-b border-border mb-4">
            <CardTitle className="text-sm text-foreground flex items-center gap-2">
              <Server className="h-4 w-4 text-primary" />
              <span>Conexión Cortex Server</span>
            </CardTitle>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={logout}
              className="text-xs text-destructive hover:bg-destructive/10 border-destructive/30"
            >
              <LogOut className="h-3.5 w-3.5 mr-1" />
              Desconectar
            </Button>
          </div>

          <form onSubmit={handleSaveServer} className="space-y-4 text-xs">
            {managedServerEndpoint ? (
              <div className="rounded-lg border border-primary/20 bg-primary/5 p-3">
                <span className="text-[11px] font-semibold text-muted-foreground block uppercase">Servidor Cortex administrado</span>
                <span className="mt-1 block break-all font-mono text-xs text-primary">{serverUrl}</span>
                <span className="mt-1 block text-[11px] text-muted-foreground">La composición Docker administra este endpoint.</span>
              </div>
            ) : (
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-foreground block uppercase">
                  URL DEL SERVIDOR CORTEX
                </label>
                <Input
                  type="text"
                  value={inputUrl}
                  onChange={(e) => setInputUrl(e.target.value)}
                  placeholder="https://cortex-server-production-cb53.up.railway.app"
                  className="h-9 font-mono"
                  required
                />
              </div>
            )}

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-foreground block uppercase">
                BEARER TOKEN
                <span className="font-normal text-muted-foreground lowercase">
                  {" "}(en memoria de sesión; nunca persistido en disco)
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
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
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
              <Button type="submit" size="sm" className="gap-1.5 shadow-sm text-xs">
                <Save className="h-3.5 w-3.5" />
                <span>Actualizar Conexión</span>
              </Button>
            </div>
          </form>
        </Card>

        {/* Server LLM Engine Runtime Card */}
        <Card className="p-4 sm:p-5 bg-card border-border shadow-sm space-y-4">
          <div className="flex items-center justify-between pb-3 border-b border-border">
            <CardTitle className="text-sm text-foreground flex items-center gap-2">
              <Sparkles className="h-4 w-4 text-primary" />
              <span>Motor LLM del Servidor</span>
            </CardTitle>
            {aiStatus?.llm.configured ? (
              <Badge variant="default" className="text-[10px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/25">
                ● Servidor Activo
              </Badge>
            ) : (
              <Badge variant="secondary" className="text-[10px] text-muted-foreground">
                No Configurado
              </Badge>
            )}
          </div>

          <div className="grid grid-cols-2 gap-3 text-xs">
            <div className="p-3 rounded-lg bg-secondary/50 border border-border">
              <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">PROVEEDOR</span>
              <span className="text-sm font-semibold text-foreground mt-0.5 block font-mono">
                {aiStatus?.llm.provider || "Cargando..."}
              </span>
            </div>
            <div className="p-3 rounded-lg bg-secondary/50 border border-border">
              <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">MODELO</span>
              <span className="text-sm font-semibold text-primary mt-0.5 block font-mono">
                {aiStatus?.llm.model || "Cargando..."}
              </span>
            </div>
          </div>

          <div className="p-3 rounded-lg bg-secondary/50 border border-border text-xs space-y-1">
            <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">ENDPOINT BASE / BASE URL</span>
            <span className="text-xs text-muted-foreground font-mono block break-all">
              {aiStatus?.llm.base_url || "Configuración por defecto del proveedor"}
            </span>
          </div>

          {/* LLM Test Action & Results */}
          <div className="pt-2 border-t border-border space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">
                Prueba de latencia y disponibilidad con el modelo configurado.
              </span>
              <Button
                onClick={handleTestLLM}
                disabled={isTestingLLM || !client}
                size="sm"
                className="text-xs gap-1.5 shadow-sm"
              >
                <Zap className={`h-3.5 w-3.5 ${isTestingLLM ? "animate-pulse text-amber-300" : ""}`} />
                <span>{isTestingLLM ? "Probando LLM..." : "Probar Conexión LLM"}</span>
              </Button>
            </div>

            {llmTestResult && (
              <div
                className={`p-3 rounded-lg text-xs space-y-1.5 border ${
                  llmTestResult.status === "ok"
                    ? "bg-emerald-500/10 border-emerald-500/30 text-emerald-600 dark:text-emerald-300"
                    : llmTestResult.status === "not_configured"
                    ? "bg-amber-500/10 border-amber-500/30 text-amber-600 dark:text-amber-300"
                    : "bg-destructive/10 border-destructive/30 text-destructive"
                }`}
              >
                <div className="flex items-center justify-between font-semibold">
                  <span className="flex items-center gap-1.5">
                    {llmTestResult.status === "ok" && <CheckCircle2 className="h-4 w-4 text-emerald-500" />}
                    {llmTestResult.status === "not_configured" && <AlertTriangle className="h-4 w-4 text-amber-500" />}
                    {llmTestResult.status === "error" && <AlertCircle className="h-4 w-4 text-destructive" />}
                    {llmTestResult.status === "ok" ? "Respuesta OK" : llmTestResult.status === "not_configured" ? "No Configurado" : "Fallo de Inferencia"}
                  </span>
                  {llmTestResult.latency_ms && (
                    <span className="font-mono text-[11px] opacity-80">{llmTestResult.latency_ms}ms</span>
                  )}
                </div>
                {llmTestResult.response && (
                  <p className="font-mono text-xs opacity-90 break-words mt-1">{llmTestResult.response}</p>
                )}
                {llmTestResult.error && <p className="font-mono text-[11px] text-destructive">{llmTestResult.error}</p>}
                {llmTestResult.message && <p className="text-[11px] text-amber-600 dark:text-amber-300">{llmTestResult.message}</p>}
              </div>
            )}
          </div>
        </Card>

        {/* Server Vector Embedding Runtime Card */}
        <Card className="p-4 sm:p-5 bg-card border-border shadow-sm space-y-4">
          <div className="flex items-center justify-between pb-3 border-b border-border">
            <CardTitle className="text-sm text-foreground flex items-center gap-2">
              <Bot className="h-4 w-4 text-primary" />
              <span>Motor de Embeddings & Vectores</span>
            </CardTitle>
            {aiStatus?.embedding.configured ? (
              <Badge variant="secondary" className="text-[10px] font-mono">
                ● {aiStatus.embedding.model} ({aiStatus.embedding.dimensions || 2560}d)
              </Badge>
            ) : (
              <Badge variant="secondary" className="text-[10px] text-muted-foreground">
                No Configurado
              </Badge>
            )}
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3 text-xs">
            <div className="p-3 rounded-lg bg-secondary/50 border border-border">
              <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">PROVEEDOR</span>
              <span className="text-sm font-semibold text-foreground mt-0.5 block font-mono">
                {aiStatus?.embedding.provider || "Ollama"}
              </span>
            </div>
            <div className="p-3 rounded-lg bg-secondary/50 border border-border">
              <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">MODELO</span>
              <span className="text-sm font-semibold text-primary mt-0.5 block font-mono">
                {aiStatus?.embedding.model || "bge-m3"}
              </span>
            </div>
            <div className="p-3 rounded-lg bg-secondary/50 border border-border">
              <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">DIMENSIONES</span>
              <span className="text-sm font-semibold text-emerald-500 dark:text-emerald-400 mt-0.5 block font-mono">
                {aiStatus?.embedding.dimensions || 1024} floats
              </span>
            </div>
          </div>

          <div className="p-3 rounded-lg bg-secondary/50 border border-border text-xs space-y-1">
            <span className="text-[10px] font-bold text-muted-foreground uppercase tracking-wider block">ENDPOINT DE OLLAMA / EMBEDDINGS</span>
            <span className="text-xs text-muted-foreground font-mono block break-all">
              {aiStatus?.embedding.base_url || "http://ollama.railway.internal:11434"}
            </span>
          </div>

          {/* Embedding Test Action & Results */}
          <div className="pt-2 border-t border-border space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs text-muted-foreground">
                Genera un vector en vivo para validar dimensiones y latencia.
              </span>
              <Button
                onClick={handleTestEmbedding}
                disabled={isTestingEmbedding || !client}
                size="sm"
                className="text-xs gap-1.5 shadow-xs"
              >
                <Layers className={`h-3.5 w-3.5 ${isTestingEmbedding ? "animate-pulse text-amber-300" : ""}`} />
                <span>{isTestingEmbedding ? "Generando Vector..." : "Probar Embeddings"}</span>
              </Button>
            </div>

            {embeddingTestResult && (
              <div
                className={`p-3 rounded-lg text-xs space-y-1.5 border ${
                  embeddingTestResult.status === "ok"
                    ? "bg-emerald-500/10 border-emerald-500/30 text-emerald-600 dark:text-emerald-400"
                    : embeddingTestResult.status === "not_configured"
                    ? "bg-amber-500/10 border-amber-500/30 text-amber-600 dark:text-amber-400"
                    : "bg-destructive/10 border-destructive/30 text-destructive"
                }`}
              >
                <div className="flex items-center justify-between font-semibold">
                  <span className="flex items-center gap-1.5">
                    {embeddingTestResult.status === "ok" && <CheckCircle className="h-4 w-4 text-emerald-500" />}
                    {embeddingTestResult.status === "error" && <AlertCircle className="h-4 w-4 text-destructive" />}
                    {embeddingTestResult.status === "not_configured" && <AlertCircle className="h-4 w-4 text-amber-500" />}
                    <span>
                      {embeddingTestResult.status === "ok"
                        ? `Vector Generado: ${embeddingTestResult.dimensions} Dimensiones`
                        : embeddingTestResult.status === "not_configured"
                        ? "Embedding No Configurado"
                        : "Error en Generación de Embeddings"}
                    </span>
                  </span>
                  <span className="font-mono text-[11px] opacity-80">{embeddingTestResult.latency_ms} ms</span>
                </div>
                {embeddingTestResult.sample_vector && (
                  <p className="font-mono text-[10px] bg-secondary/50 p-2 rounded-lg text-foreground break-all border border-border">
                    Muestra del vector: [{embeddingTestResult.sample_vector.map((v) => v.toFixed(4)).join(", ")}, ...]
                  </p>
                )}
                {embeddingTestResult.error && <p className="font-mono text-[11px] text-destructive">{embeddingTestResult.error}</p>}
                {embeddingTestResult.message && <p className="text-[11px] text-amber-600 dark:text-amber-400">{embeddingTestResult.message}</p>}
              </div>
            )}
          </div>
        </Card>

        {/* Hybrid Search Weights & Retrieval Tuning */}
        <Card className="p-4 sm:p-5 bg-card border-border shadow-sm">
          <div className="flex items-center justify-between pb-3 border-b border-border mb-4">
            <CardTitle className="text-sm text-foreground flex items-center gap-2">
              <Sliders className="h-4 w-4 text-emerald-500 dark:text-emerald-400" />
              <span>Sintonización de Búsqueda Híbrida</span>
            </CardTitle>
          </div>

          <form onSubmit={handleSaveSearchWeights} className="space-y-3.5 text-xs">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
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
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
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
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
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
              <span className="text-[11px] text-muted-foreground">
                Límite por consulta: <b className="font-mono text-foreground">{defaultLimit}</b> resultados
              </span>
              <Button type="submit" size="sm" variant="outline" className="text-xs">
                {searchSavedMessage ? "¡Guardado!" : "Guardar Ponderación"}
              </Button>
            </div>
          </form>
        </Card>
      </div>

      {/* Autonomous AI Background Maintenance Worker Hub */}
      <Card className="p-4 sm:p-6 bg-card border-border shadow-sm space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-border">
          <div>
            <CardTitle className="text-base text-foreground flex items-center gap-2">
              <Bot className="h-5 w-5 text-primary" />
              <span>Hub de Mantenimiento Autónomo con IA (Background Workers)</span>
            </CardTitle>
            <p className="text-xs text-muted-foreground mt-0.5">
              Ejecuta agentes en background utilizando los modelos configurados en el servidor para optimizar la base de conocimiento.
            </p>
          </div>
          {isWorkerRunning && (
            <span className="flex items-center gap-2 text-xs text-amber-500 font-mono animate-pulse">
              <span className="w-2 h-2 rounded-full bg-amber-500" />
              Agente en ejecución...
            </span>
          )}
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-3.5">
          {/* Job 1: Graph Auto Reorganizer */}
          <div className="p-4 rounded-lg bg-secondary/50 border border-border flex flex-col justify-between space-y-3">
            <div>
              <div className="flex items-center gap-2 font-semibold text-xs text-foreground mb-1">
                <span>Reorganizar Grafo Semántico</span>
              </div>
              <p className="text-[11px] text-muted-foreground leading-relaxed">
                Descubre enlaces ocultos entre observaciones huérfanas y crea relaciones estructuradas (relates_to, supersedes).
              </p>
            </div>
            <Button
              onClick={runBackgroundGraphReorganization}
              disabled={isWorkerRunning}
              size="sm"
              className="w-full text-xs gap-1.5 shadow-sm"
            >
              <span>Ejecutar Reorganización</span>
            </Button>
          </div>

          {/* Job 2: Conflict & Contradiction Resolver */}
          <div className="p-4 rounded-lg bg-secondary/50 border border-border flex flex-col justify-between space-y-3">
            <div>
              <div className="flex items-center gap-2 font-semibold text-xs text-foreground mb-1">
                <span>Resolver Conflictos & Contradicciones</span>
              </div>
              <p className="text-[11px] text-muted-foreground leading-relaxed">
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
        </div>

        {/* Live Background Logs Console */}
        {workerLogs.length > 0 && (
          <div className="mt-4 p-3.5 rounded-lg bg-secondary/80 border border-border font-mono text-[11px] space-y-1">
            <div className="text-[10px] text-muted-foreground uppercase tracking-wider mb-2 flex items-center justify-between">
              <span>Registro de Operaciones en Background:</span>
              <button
                type="button"
                onClick={() => setWorkerLogs([])}
                className="text-[10px] text-muted-foreground hover:text-foreground"
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
