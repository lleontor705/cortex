"use client";

import React, { useEffect, useState, useMemo } from "react";
import { useAuth } from "@/lib/auth-context";
import {
  CodeSymbol,
  CodeRelation,
  CodeGraph,
  CodeAnalyticsReport,
  GodNode,
  ImportCycle,
  CommunityCohesion,
} from "@/lib/api";
import { Card, CardHeader, CardTitle, CardContent, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogHeader, DialogTitle, DialogClose } from "@/components/ui/dialog";
import { PageHeader } from "@/components/shared/PageHeader";
import { StatCard } from "@/components/shared/StatCard";
import { EmptyState } from "@/components/shared/EmptyState";
import { StatusBadge } from "@/components/shared/StatusBadge";
import {
  Terminal,
  Code2,
  GitFork,
  AlertTriangle,
  Layers,
  Search,
  RefreshCw,
  Play,
  FileCode,
  Box,
  Share2,
  CheckCircle2,
  Zap,
  Info,
  Folder,
  ArrowRight,
  Copy,
  Check,
  SlidersHorizontal,
  X,
} from "lucide-react";

export default function CodeExplorerPage() {
  const { client } = useAuth();

  const [projects, setProjects] = useState<string[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>("default");
  const [symbols, setSymbols] = useState<CodeSymbol[]>([]);
  const [analytics, setAnalytics] = useState<CodeAnalyticsReport | null>(null);
  const [graph, setGraph] = useState<CodeGraph | null>(null);

  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [isIngesting, setIsIngesting] = useState<boolean>(false);
  const [activeTab, setActiveTab] = useState<"god_nodes" | "cycles" | "cohesion" | "symbols" | "graph">("god_nodes");

  // Ingestion Form State
  const [scanPath, setScanPath] = useState<string>(".");
  const [maxFiles, setMaxFiles] = useState<number>(500);
  const [ingestSuccess, setIngestSuccess] = useState<string | null>(null);

  // Search & Filter State
  const [searchQuery, setSearchQuery] = useState<string>("");
  const [kindFilter, setKindFilter] = useState<string>("all");
  const [sortBy, setSortBy] = useState<"name" | "line" | "kind">("name");
  const [selectedSymbol, setSelectedSymbol] = useState<CodeSymbol | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Fetch projects list
  useEffect(() => {
    if (!client) return;
    client.projects()
      .then((res) => {
        if (res && res.length > 0) {
          setProjects(res);
          if (!res.includes(selectedProject)) {
            setSelectedProject(res[0]);
          }
        } else {
          setProjects(["default"]);
        }
      })
      .catch(() => setProjects(["default"]));
  }, [client]);

  // Load symbols and analytics whenever project changes
  const loadCodeData = async () => {
    if (!client) return;
    setIsLoading(true);
    try {
      const [syms, rep, gr] = await Promise.all([
        client.getCodeSymbols({ project: selectedProject, limit: 1000 }).catch(() => []),
        client.getCodeAnalytics(selectedProject).catch(() => null),
        client.getCodeGraph(selectedProject).catch(() => null),
      ]);
      setSymbols(Array.isArray(syms) ? syms : []);
      setAnalytics(rep);
      setGraph(gr);
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    loadCodeData();
  }, [client, selectedProject]);

  const handleIngest = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client) return;
    setIsIngesting(true);
    setIngestSuccess(null);
    try {
      const rep = await client.ingestCodeAST({
        path: scanPath,
        project: selectedProject,
        max_files: maxFiles,
      });
      setAnalytics(rep);
      setIngestSuccess(`¡Escaneo exitoso! ${rep?.total_symbols ?? 0} símbolos y ${rep?.total_relations ?? 0} relaciones indexadas.`);
      await loadCodeData();
    } catch (err: any) {
      setIngestSuccess(`Error al escanear: ${err.message || err}`);
    } finally {
      setIsIngesting(false);
    }
  };

  const handleCopy = (text: string, id: string) => {
    navigator.clipboard.writeText(text);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 2000);
  };

  // Filtered and sorted symbols
  const filteredSymbols = useMemo(() => {
    if (!Array.isArray(symbols)) return [];
    const list = symbols.filter((s) => {
      if (!s) return false;
      const name = s.name || "";
      const filePath = s.file_path || "";
      const signature = s.signature || "";
      const matchesSearch =
        searchQuery === "" ||
        name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        filePath.toLowerCase().includes(searchQuery.toLowerCase()) ||
        signature.toLowerCase().includes(searchQuery.toLowerCase());
      const matchesKind = kindFilter === "all" || s.kind === kindFilter;
      return matchesSearch && matchesKind;
    });

    return list.sort((a, b) => {
      if (sortBy === "name") return (a.name || "").localeCompare(b.name || "");
      if (sortBy === "line") return (a.line_number || 0) - (b.line_number || 0);
      if (sortBy === "kind") return (a.kind || "").localeCompare(b.kind || "");
      return 0;
    });
  }, [symbols, searchQuery, kindFilter, sortBy]);

  const symbolKinds = useMemo(() => {
    if (!Array.isArray(symbols)) return [];
    const set = new Set(symbols.map((s) => s.kind).filter(Boolean));
    return Array.from(set);
  }, [symbols]);

  const getKindColor = (kind?: string) => {
    switch ((kind || "").toLowerCase()) {
      case "func":
      case "function":
      case "method":
        return "bg-blue-500/10 text-blue-400 border-blue-500/20";
      case "struct":
      case "class":
        return "bg-purple-500/10 text-purple-400 border-purple-500/20";
      case "interface":
        return "bg-amber-500/10 text-amber-400 border-amber-500/20";
      case "module":
      case "package":
        return "bg-emerald-500/10 text-emerald-400 border-emerald-500/20";
      case "table":
        return "bg-cyan-500/10 text-cyan-400 border-cyan-500/20";
      default:
        return "bg-zinc-500/10 text-zinc-400 border-zinc-500/20";
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <PageHeader
        title="Código & AST Explorer"
        badge={
          <Badge variant="outline" className="bg-primary/5 text-primary border-primary/20 text-xs font-mono">
            Graphify Engine
          </Badge>
        }
        description="Grafo determinista de símbolos, Hubs Arquitectónicos (God Nodes), Ciclos y Cohesión de módulos."
        actions={
          <div className="flex items-center gap-2.5">
            <div className="flex items-center gap-2 bg-card border border-border rounded-lg px-2.5 py-1 shadow-xs">
              <Folder className="h-3.5 w-3.5 text-muted-foreground" />
              <select
                value={selectedProject}
                onChange={(e) => setSelectedProject(e.target.value)}
                className="bg-transparent text-xs font-medium focus:outline-none cursor-pointer"
              >
                {projects.map((p) => (
                  <option key={p} value={p} className="bg-popover text-popover-foreground">
                    {p}
                  </option>
                ))}
              </select>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={loadCodeData}
              disabled={isLoading}
              className="flex items-center gap-1.5 h-8 text-xs"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${isLoading ? "animate-spin" : ""}`} />
              <span>Actualizar</span>
            </Button>
          </div>
        }
      />

      {/* Metrics Row */}
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3.5">
        <StatCard
          title="Símbolos AST"
          value={analytics?.total_symbols ?? symbols?.length ?? 0}
          icon={Code2}
          iconClassName="text-primary"
          subtext="Funciones, tipos, tablas"
        />

        <StatCard
          title="Relaciones"
          value={analytics?.total_relations ?? graph?.relations?.length ?? 0}
          icon={Share2}
          iconClassName="text-purple-500 dark:text-purple-400"
          subtext="Llamadas y usos resueltos"
        />

        <StatCard
          title="Archivos Código"
          value={analytics?.total_files ?? 0}
          icon={FileCode}
          iconClassName="text-emerald-500 dark:text-emerald-400"
          subtext="Módulos escaneados"
        />

        <StatCard
          title="Cohesión Promedio"
          value={analytics?.average_cohesion ? (analytics.average_cohesion * 100).toFixed(0) + "%" : "N/A"}
          icon={Layers}
          iconClassName="text-cyan-500 dark:text-cyan-400"
          subtext="Densidad modular"
        />

        <StatCard
          title="God Nodes"
          value={analytics?.god_nodes?.length ?? 0}
          icon={Zap}
          iconClassName="text-amber-500 dark:text-amber-400"
          subtext="Hubs arquitectónicos"
        />

        <StatCard
          title="Ciclos"
          value={analytics?.import_cycles?.length ?? 0}
          icon={AlertTriangle}
          iconClassName={(analytics?.import_cycles?.length || 0) > 0 ? "text-destructive" : "text-emerald-500"}
          subtext="Dependencias circulares"
        />
      </div>

      {/* Ingestion Trigger Bar */}
      <Card className="bg-card/40 border-border/60 shadow-sm">
        <CardContent className="p-4">
          <form onSubmit={handleIngest} className="flex flex-col md:flex-row items-center gap-3">
            <div className="flex-1 flex items-center gap-2 w-full">
              <Terminal className="h-4 w-4 text-muted-foreground shrink-0" />
              <Input
                placeholder="Ruta local o repo (ej: . o internal/domain)"
                value={scanPath}
                onChange={(e) => setScanPath(e.target.value)}
                className="bg-background text-xs h-9"
              />
            </div>
            <div className="flex items-center gap-2 w-full md:w-auto">
              <span className="text-xs text-muted-foreground shrink-0">Max Files:</span>
              <Input
                type="number"
                value={maxFiles}
                onChange={(e) => setMaxFiles(parseInt(e.target.value) || 500)}
                className="w-24 bg-background text-xs h-9"
                min={1}
                max={2000}
              />
              <Button type="submit" disabled={isIngesting} size="sm" className="h-9 shrink-0 flex items-center gap-2">
                <Play className={`h-3.5 w-3.5 ${isIngesting ? "animate-spin" : ""}`} />
                {isIngesting ? "Escaneando..." : "Escanear AST"}
              </Button>
            </div>
          </form>
          {ingestSuccess && (
            <div className="mt-3 text-xs p-2.5 rounded-md bg-primary/10 border border-primary/20 text-primary flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 shrink-0" />
              {ingestSuccess}
            </div>
          )}
        </CardContent>
      </Card>

      {/* Navigation Tabs */}
      <div className="flex items-center gap-2 border-b border-border/60 pb-2 overflow-x-auto">
        <Button
          variant={activeTab === "god_nodes" ? "default" : "ghost"}
          size="sm"
          onClick={() => setActiveTab("god_nodes")}
          className="flex items-center gap-2 text-xs"
        >
          <Zap className="h-3.5 w-3.5" />
          Hubs Arquitectónicos (God Nodes)
          {analytics?.god_nodes?.length ? (
            <Badge variant="secondary" className="ml-1 px-1.5 py-0 text-[10px]">
              {analytics.god_nodes.length}
            </Badge>
          ) : null}
        </Button>

        <Button
          variant={activeTab === "cycles" ? "default" : "ghost"}
          size="sm"
          onClick={() => setActiveTab("cycles")}
          className="flex items-center gap-2 text-xs"
        >
          <AlertTriangle className="h-3.5 w-3.5" />
          Ciclos de Dependencias
          {analytics?.import_cycles?.length ? (
            <Badge variant="destructive" className="ml-1 px-1.5 py-0 text-[10px]">
              {analytics.import_cycles.length}
            </Badge>
          ) : null}
        </Button>

        <Button
          variant={activeTab === "cohesion" ? "default" : "ghost"}
          size="sm"
          onClick={() => setActiveTab("cohesion")}
          className="flex items-center gap-2 text-xs"
        >
          <Layers className="h-3.5 w-3.5" />
          Cohesión Modular ({analytics?.communities?.length ?? 0})
        </Button>

        <Button
          variant={activeTab === "symbols" ? "default" : "ghost"}
          size="sm"
          onClick={() => setActiveTab("symbols")}
          className="flex items-center gap-2 text-xs"
        >
          <Code2 className="h-3.5 w-3.5" />
          Símbolos AST ({symbols?.length ?? 0})
        </Button>
      </div>

      {/* Tab 1: God Nodes */}
      {activeTab === "god_nodes" && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-semibold text-foreground">Hubs y Nodos Centrales</h2>
              <p className="text-xs text-muted-foreground">
                Símbolos con mayor grado de centralidad e impacto arquitectónico en el grafo de llamadas.
              </p>
            </div>
          </div>

          {Array.isArray(analytics?.god_nodes) && analytics.god_nodes.length > 0 ? (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {analytics.god_nodes.map((gn, idx) => (
                <Card
                  key={gn.id}
                  className="bg-card hover:bg-card/80 border-border/60 transition cursor-pointer hover:border-primary/40 shadow-sm"
                  onClick={() => {
                    const name = gn.name || gn.label || "";
                    const found = symbols.find((s) => s && (s.id === gn.id || (name && s.name === name)));
                    if (found) setSelectedSymbol(found);
                  }}
                >
                  <CardHeader className="p-4 pb-2">
                    <div className="flex items-center justify-between">
                      <Badge variant="outline" className={`text-xs capitalize ${getKindColor(gn.kind)}`}>
                        {gn.kind || "symbol"}
                      </Badge>
                      <div className="flex items-center gap-1.5">
                        <span className="text-[10px] font-mono text-muted-foreground">Rank #{idx + 1}</span>
                        <Badge variant="secondary" className="text-xs font-mono font-bold">
                          Score: {gn.score ? gn.score.toFixed(1) : gn.degree}
                        </Badge>
                      </div>
                    </div>
                    <CardTitle className="text-base font-mono font-bold mt-2 truncate text-foreground">
                      {gn.name || gn.label || gn.id}
                    </CardTitle>
                    <CardDescription className="text-xs font-mono truncate text-muted-foreground">
                      {gn.file_path || gn.source_file || ""}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="p-4 pt-2">
                    <div className="grid grid-cols-3 gap-2 py-2 border-t border-border/40 text-center text-xs font-mono mt-2">
                      <div className="bg-background/60 p-1.5 rounded">
                        <div className="text-[10px] text-muted-foreground">Grado Total</div>
                        <div className="font-bold text-foreground">{gn.degree}</div>
                      </div>
                      <div className="bg-background/60 p-1.5 rounded">
                        <div className="text-[10px] text-muted-foreground">In (Callers)</div>
                        <div className="font-bold text-blue-400">{gn.in_degree}</div>
                      </div>
                      <div className="bg-background/60 p-1.5 rounded">
                        <div className="text-[10px] text-muted-foreground">Out (Callees)</div>
                        <div className="font-bold text-purple-400">{gn.out_degree}</div>
                      </div>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <EmptyState
              icon={Zap}
              title="No se detectaron God Nodes aún"
              description="Ejecuta 'Escanear AST' para indexar la estructura del proyecto e identificar los hubs con mayor acoplamiento."
            />
          )}
        </div>
      )}

      {/* Tab 2: Circular Dependencies */}
      {activeTab === "cycles" && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-semibold text-foreground">Dependencias Circulares Detectadas</h2>
              <p className="text-xs text-muted-foreground">
                Ciclos en el grafo de llamadas o importaciones que comprometen la modularidad y testing aislado.
              </p>
            </div>
          </div>

          {Array.isArray(analytics?.import_cycles) && analytics.import_cycles.length > 0 ? (
            <div className="space-y-3">
              {analytics.import_cycles.map((cyc, idx) => (
                <Card key={idx} className="bg-destructive/5 border-destructive/30 shadow-xs">
                  <CardContent className="p-4">
                    <div className="flex items-center justify-between mb-3">
                      <div className="flex items-center gap-2">
                        <AlertTriangle className="h-4 w-4 text-destructive" />
                        <span className="font-semibold text-sm text-foreground">Ciclo #{idx + 1}</span>
                      </div>
                      <Badge variant="outline" className="bg-destructive/10 text-destructive border-destructive/20 text-xs">
                        Longitud: {cyc?.length ?? cyc?.nodes?.length ?? 0} nodos
                      </Badge>
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-xs font-mono bg-background/60 p-3 rounded-lg border border-border/40">
                      {Array.isArray(cyc?.nodes) && cyc.nodes.map((node, nIdx) => (
                        <React.Fragment key={nIdx}>
                          <span className="px-2 py-1 rounded bg-card border border-border text-foreground font-medium">
                            {node}
                          </span>
                          {nIdx < cyc.nodes.length - 1 && (
                            <ArrowRight className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                          )}
                        </React.Fragment>
                      ))}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <Card className="p-8 text-center bg-emerald-500/5 border-emerald-500/20 border rounded-lg">
              <CheckCircle2 className="h-8 w-8 text-emerald-500 mx-auto mb-2" />
              <h3 className="text-sm font-semibold text-foreground">¡Arquitectura Limpia!</h3>
              <p className="text-xs text-muted-foreground mt-1">
                No se detectaron ciclos de importación o llamadas recursivas entre paquetes.
              </p>
            </Card>
          )}
        </div>
      )}

      {/* Tab 3: Community Cohesion */}
      {activeTab === "cohesion" && (
        <div className="space-y-4">
          <div className="flex items-center justify-between">
            <div>
              <h2 className="text-lg font-semibold text-foreground">Cohesión de Módulos & Comunidades</h2>
              <p className="text-xs text-muted-foreground">
                Puntaje de densidad de llamadas internas por paquete (Fórmula Graphify: C = 2*E / (N*(N-1))).
              </p>
            </div>
          </div>

          {Array.isArray(analytics?.communities) && analytics.communities.length > 0 ? (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {analytics.communities.map((com, idx) => (
                <Card key={idx} className="bg-card border-border shadow-xs">
                  <CardHeader className="p-4 pb-2">
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-2">
                        <Box className="h-4 w-4 text-primary" />
                        <CardTitle className="text-sm font-mono font-bold truncate text-foreground">
                          {com.name}
                        </CardTitle>
                      </div>
                      <Badge
                        variant="outline"
                        className={`text-xs font-mono font-bold ${
                          com.cohesion_score >= 0.6
                            ? "bg-emerald-500/10 text-emerald-500 border-emerald-500/20"
                            : com.cohesion_score >= 0.3
                            ? "bg-amber-500/10 text-amber-500 border-amber-500/20"
                            : "bg-zinc-500/10 text-muted-foreground border-zinc-500/20"
                        }`}
                      >
                        Cohesión: {(com.cohesion_score * 100).toFixed(0)}%
                      </Badge>
                    </div>
                  </CardHeader>
                  <CardContent className="p-4 pt-2 space-y-3">
                    <div className="w-full bg-secondary rounded-full h-2 overflow-hidden">
                      <div
                        className={`h-full rounded-full transition-all ${
                          com.cohesion_score >= 0.6
                            ? "bg-emerald-500"
                            : com.cohesion_score >= 0.3
                            ? "bg-amber-500"
                            : "bg-zinc-500"
                        }`}
                        style={{ width: `${Math.min(100, Math.max(5, com.cohesion_score * 100))}%` }}
                      />
                    </div>
                    <div className="flex justify-between text-xs text-muted-foreground font-mono">
                      <span>{com.symbol_count} símbolos</span>
                      <span>{com.internal_edges} relaciones internas</span>
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <EmptyState
              icon={Layers}
              title="No hay comunidades calculadas aún"
              description="Escanea el AST para calcular la densidad de relaciones internas y la cohesión modular."
            />
          )}
        </div>
      )}

      {/* Tab 4: Symbol Explorer */}
      {activeTab === "symbols" && (
        <div className="space-y-4">
          <div className="flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
            <div className="relative w-full sm:w-80">
              <Search className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input
                placeholder="Buscar símbolo, archivo o firma..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="pl-9 pr-8 bg-card text-xs h-9"
              />
              {searchQuery && (
                <button
                  type="button"
                  onClick={() => setSearchQuery("")}
                  className="absolute right-2.5 top-2.5 text-muted-foreground hover:text-foreground"
                >
                  <X className="h-4 w-4" />
                </button>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <div className="flex items-center gap-1 bg-secondary/50 border border-border px-2.5 py-1 rounded-lg text-xs">
                <SlidersHorizontal className="h-3.5 w-3.5 text-muted-foreground mr-1" />
                <span className="text-muted-foreground font-medium mr-1">Orden:</span>
                <button
                  type="button"
                  onClick={() => setSortBy("name")}
                  className={`px-2 py-0.5 rounded text-[11px] font-medium transition-colors ${
                    sortBy === "name" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  Nombre
                </button>
                <button
                  type="button"
                  onClick={() => setSortBy("line")}
                  className={`px-2 py-0.5 rounded text-[11px] font-medium transition-colors ${
                    sortBy === "line" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  Línea
                </button>
                <button
                  type="button"
                  onClick={() => setSortBy("kind")}
                  className={`px-2 py-0.5 rounded text-[11px] font-medium transition-colors ${
                    sortBy === "kind" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  Tipo
                </button>
              </div>

              <div className="flex items-center gap-1.5 overflow-x-auto">
                <Button
                  variant={kindFilter === "all" ? "default" : "outline"}
                  size="sm"
                  onClick={() => setKindFilter("all")}
                  className="text-xs h-8"
                >
                  Todos ({symbols?.length ?? 0})
                </Button>
                {symbolKinds.map((k) => (
                  <Button
                    key={k}
                    variant={kindFilter === k ? "default" : "outline"}
                    size="sm"
                    onClick={() => setKindFilter(k)}
                    className="text-xs h-8 capitalize"
                  >
                    {k}
                  </Button>
                ))}
              </div>
            </div>
          </div>

          {filteredSymbols.length === 0 ? (
            <EmptyState
              icon={Code2}
              title={searchQuery ? "Sin coincidencias de símbolos" : "No hay símbolos indexados"}
              description={
                searchQuery
                  ? `No se encontró ningún símbolo que coincida con "${searchQuery}". Intenta con otro término o limpia la búsqueda.`
                  : "Ejecuta 'Escanear AST' para descubrir funciones, structs, interfaces y tablas en el proyecto."
              }
              action={
                searchQuery ? (
                  <Button variant="outline" size="sm" onClick={() => setSearchQuery("")} className="text-xs">
                    Limpiar Búsqueda
                  </Button>
                ) : null
              }
            />
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
              {filteredSymbols.map((sym) => (
                <Card
                  key={sym.id}
                  className="bg-card hover:bg-card/80 border-border transition cursor-pointer hover:border-primary/40 shadow-xs"
                  onClick={() => setSelectedSymbol(sym)}
                >
                  <CardHeader className="p-4 pb-2">
                    <div className="flex items-center justify-between">
                      <Badge variant="outline" className={`text-xs capitalize ${getKindColor(sym.kind)}`}>
                        {sym.kind}
                      </Badge>
                      <span className="text-[10px] font-mono text-muted-foreground">Línea {sym.line_number}</span>
                    </div>
                    <CardTitle className="text-sm font-mono font-bold mt-1.5 truncate text-foreground">
                      {sym.name}
                    </CardTitle>
                    <CardDescription className="text-xs font-mono truncate text-muted-foreground">
                      {sym.file_path}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className="p-4 pt-1">
                    {sym.signature && (
                      <div className="text-[11px] font-mono bg-secondary/50 p-2 rounded border border-border text-foreground/90 truncate">
                        {sym.signature}
                      </div>
                    )}
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Symbol Detail Modal */}
      {selectedSymbol && (
        <Dialog open={Boolean(selectedSymbol)} onOpenChange={() => setSelectedSymbol(null)} className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              <span className={`px-2 py-0.5 rounded text-xs uppercase font-mono border ${getKindColor(selectedSymbol.kind)}`}>
                {selectedSymbol.kind}
              </span>
              <span className="font-mono text-base font-bold ml-2 text-foreground">{selectedSymbol.name}</span>
            </DialogTitle>
            <DialogClose onClick={() => setSelectedSymbol(null)} />
          </DialogHeader>

          <div className="space-y-4 pt-2">
            <div className="text-xs font-mono text-muted-foreground">
              {selectedSymbol.file_path}:{selectedSymbol.line_number}
            </div>

            {selectedSymbol.signature && (
              <div>
                <div className="text-xs font-semibold text-muted-foreground mb-1">Firma / Tipo:</div>
                <div className="p-3 bg-secondary/40 font-mono text-xs rounded-lg border border-border text-foreground flex items-center justify-between">
                  <code className="text-primary font-semibold">{selectedSymbol.signature}</code>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => handleCopy(selectedSymbol.signature || "", selectedSymbol.id)}
                    className="h-7 w-7 p-0 shrink-0 hover:bg-secondary"
                  >
                    {copiedId === selectedSymbol.id ? (
                      <Check className="h-3.5 w-3.5 text-emerald-500" />
                    ) : (
                      <Copy className="h-3.5 w-3.5 text-muted-foreground" />
                    )}
                  </Button>
                </div>
              </div>
            )}

            {selectedSymbol.doc_summary && (
              <div>
                <div className="text-xs font-semibold text-muted-foreground mb-1">Descripción:</div>
                <p className="text-sm text-foreground bg-secondary/40 p-3 rounded-lg border border-border">
                  {selectedSymbol.doc_summary}
                </p>
              </div>
            )}

            <div className="grid grid-cols-2 gap-3 text-xs font-mono bg-secondary/40 p-3 rounded-lg border border-border">
              <div>
                <span className="text-muted-foreground">Paquete:</span>{" "}
                <span className="text-foreground font-semibold">{selectedSymbol.package_name || "main"}</span>
              </div>
              <div>
                <span className="text-muted-foreground">Proyecto:</span>{" "}
                <span className="text-foreground font-semibold">{selectedSymbol.project}</span>
              </div>
              {selectedSymbol.file_hash && (
                <div className="col-span-2 truncate">
                  <span className="text-muted-foreground">SHA-256 Hash:</span>{" "}
                  <span className="text-muted-foreground text-[10px]">{selectedSymbol.file_hash}</span>
                </div>
              )}
            </div>
          </div>
        </Dialog>
      )}
    </div>
  );
}
