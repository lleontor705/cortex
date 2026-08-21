"use client";

import React, { useEffect, useRef, useState, useCallback, useMemo, Suspense } from "react";
import { useSearchParams } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  Observation,
  GraphSubgraph,
  GraphNode,
  GraphLink,
  GraphAnalyticsReport,
  BlastRadiusResult,
} from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Dialog, DialogHeader, DialogTitle, DialogClose } from "@/components/ui/dialog";
import {
  Share2,
  ZoomIn,
  ZoomOut,
  RotateCcw,
  ArrowRight,
  Info,
  X,
  Search,
  Maximize2,
  Flame,
  Link as LinkIcon,
  Copy,
  Check,
  Filter,
  Zap,
  Activity,
  Download,
  Network,
  Radio,
  Layers,
  Sparkles,
  ShieldAlert,
} from "lucide-react";

import Graph from "graphology";
import Sigma from "sigma";
import forceAtlas2 from "graphology-layout-forceatlas2";
import { circular } from "graphology-layout";

const RELATION_COLORS: Record<string, string> = {
  references: "#3b82f6",
  supersedes: "#10b981",
  contradicts: "#ef4444",
  follows: "#f59e0b",
  relates_to: "#8b5cf6",
  caused_by: "#ec4899",
};

const DEFAULT_RELATION_COLOR = "#64748b";

const KIND_COLORS: Record<string, { bg: string; border: string; text: string; hex: string; variant: "default" | "destructive" | "success" | "warning" | "purple" | "secondary" }> = {
  decision: { bg: "#1e3a8a", border: "#3b82f6", text: "#93c5fd", hex: "#3b82f6", variant: "default" },
  bugfix: { bg: "#7f1d1d", border: "#ef4444", text: "#fca5a5", hex: "#ef4444", variant: "destructive" },
  pattern: { bg: "#064e3b", border: "#10b981", text: "#6ee7b7", hex: "#10b981", variant: "success" },
  discovery: { bg: "#78350f", border: "#f59e0b", text: "#fcd34d", hex: "#f59e0b", variant: "warning" },
  learning: { bg: "#4c1d95", border: "#8b5cf6", text: "#c4b5fd", hex: "#8b5cf6", variant: "purple" },
  config: { bg: "#164e63", border: "#06b6d4", text: "#a5f3fc", hex: "#06b6d4", variant: "default" },
  session: { bg: "#134e4a", border: "#14b8a6", text: "#5eead4", hex: "#14b8a6", variant: "success" },
  observation: { bg: "#1e293b", border: "#475569", text: "#cbd5e1", hex: "#64748b", variant: "secondary" },
};

function GraphPageContent() {
  const { client } = useAuth();
  const searchParams = useSearchParams();
  const projectParam = searchParams.get("project");

  const containerRef = useRef<HTMLDivElement | null>(null);
  const sigmaRef = useRef<Sigma | null>(null);
  const graphRef = useRef<Graph | null>(null);

  // Projects State
  const [projects, setProjects] = useState<string[]>(["default"]);
  const [selectedProject, setSelectedProject] = useState<string>(projectParam || "default");

  // Raw Graph & Observations State
  const [rawSubgraph, setRawSubgraph] = useState<GraphSubgraph | null>(null);
  const [observations, setObservations] = useState<Observation[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Selection & Hover
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");

  // Type Filters
  const [typeFilters, setTypeFilters] = useState<Record<string, boolean>>({
    decision: true,
    bugfix: true,
    pattern: true,
    discovery: true,
    learning: true,
    config: true,
    session: true,
    observation: true,
  });

  // Modals & Panels
  const [analyticsReport, setAnalyticsReport] = useState<GraphAnalyticsReport | null>(null);
  const [isAnalyticsOpen, setIsAnalyticsOpen] = useState(false);
  const [analyticsLoading, setAnalyticsLoading] = useState(false);

  const [blastData, setBlastData] = useState<BlastRadiusResult | null>(null);
  const [isBlastOpen, setIsBlastOpen] = useState(false);
  const [blastLoading, setBlastLoading] = useState(false);

  const [isConnectModalOpen, setIsConnectModalOpen] = useState(false);
  const [isResolveModalOpen, setIsResolveModalOpen] = useState(false);
  const [targetObsId, setTargetObsId] = useState("");
  const [relationType, setRelationType] = useState<string>("relates_to");
  const [relationReason, setRelationReason] = useState("");
  const [obsoleteObsId, setObsoleteObsId] = useState("");
  const [resolveReason, setResolveReason] = useState("");
  const [isConnecting, setIsConnecting] = useState(false);
  const [isResolving, setIsResolving] = useState(false);

  // Helper to normalize IDs
  const normalizeId = useCallback((id: string) => {
    if (!id) return "";
    return id.replace(/^observation:/, "").trim();
  }, []);

  // Fetch project graph data
  const loadProjectGraph = useCallback(
    async (project: string) => {
      if (!client) return;
      setLoading(true);
      setError(null);
      try {
        const data: GraphSubgraph = await client.projectGraph(project === "all" ? undefined : project, 300);
        setRawSubgraph(data);
      } catch (err: any) {
        setError(err.message || "Error al cargar grafo de conocimiento");
      } finally {
        setLoading(false);
      }
    },
    [client],
  );

  // Fetch initial project list and observations
  useEffect(() => {
    if (!client) return;
    client
      .projects()
      .then((p) => {
        const list = Array.isArray(p) ? p : ["default"];
        if (!list.includes("default")) list.unshift("default");
        setProjects(list);
      })
      .catch(() => {});

    client
      .listObservations("?limit=200")
      .then((res) => {
        setObservations(res || []);
      })
      .catch(() => {});
  }, [client]);

  useEffect(() => {
    loadProjectGraph(selectedProject);
  }, [selectedProject, loadProjectGraph]);

  // Build Sigma Graphology Instance
  useEffect(() => {
    if (!containerRef.current || !rawSubgraph) return;

    if (sigmaRef.current) {
      sigmaRef.current.kill();
      sigmaRef.current = null;
    }

    const graph = new Graph();
    graphRef.current = graph;

    const nodes = rawSubgraph.nodes || [];
    const edges = rawSubgraph.edges || [];

    // Add Nodes
    nodes.forEach((n, idx) => {
      const id = normalizeId(n.id) || n.id;
      if (graph.hasNode(id)) return;

      const kind = (n.kind || "observation").toLowerCase();
      const kindInfo = KIND_COLORS[kind] || KIND_COLORS.observation;
      const score = (typeof n.metadata?.importance_score === "number" ? n.metadata.importance_score : 0.5);
      const size = Math.max(7, Math.min(22, 9 + score * 12));

      const angle = (idx / Math.max(1, nodes.length)) * 2 * Math.PI;
      const radius = 100 + (idx % 6) * 35;
      const x = Math.cos(angle) * radius + (Math.random() - 0.5) * 20;
      const y = Math.sin(angle) * radius + (Math.random() - 0.5) * 20;

      graph.addNode(id, {
        x,
        y,
        size,
        label: n.label || id.slice(0, 8),
        color: kindInfo.hex,
        kind,
        raw: n,
      });
    });

    // Add Edges
    edges.forEach((l, idx) => {
      const source = normalizeId(l.source);
      const target = normalizeId(l.target);

      if (graph.hasNode(source) && graph.hasNode(target)) {
        const edgeId = `e-${source}-${target}-${idx}`;
        if (!graph.hasEdge(edgeId)) {
          const relType = (l.type || "relates_to").toLowerCase();
          const edgeColor = RELATION_COLORS[relType] || DEFAULT_RELATION_COLOR;
          try {
            graph.addEdgeWithKey(edgeId, source, target, {
              size: Math.max(1.5, Math.min(4.5, (l.weight || 1) * 2.2)),
              color: edgeColor,
              type: "arrow",
              relation: relType,
              raw: l,
            });
          } catch {
            // Ignore duplicate edges in graphology
          }
        }
      }
    });

    // Apply ForceAtlas2 organic layout
    if (graph.order > 1) {
      try {
        forceAtlas2.assign(graph, {
          iterations: 120,
          settings: {
            gravity: 1.2,
            scalingRatio: 8,
            slowDown: 3,
            barnesHutOptimize: true,
          },
        });
      } catch {
        circular.assign(graph);
      }
    }

    // Initialize Sigma WebGL Renderer
    const renderer = new Sigma(graph, containerRef.current, {
      minCameraRatio: 0.1,
      maxCameraRatio: 10,
      renderEdgeLabels: true,
      enableEdgeEvents: true,
      labelFont: "system-ui, -apple-system, sans-serif",
      labelSize: 11,
      labelWeight: "600",
      labelColor: { color: "#e2e8f0" },
      zIndex: true,
      stagePadding: 50,
      defaultEdgeType: "arrow",
    });

    sigmaRef.current = renderer;

    // Custom Node Reducer
    renderer.setSetting("nodeReducer", (node, data) => {
      const res = { ...data };
      const rawKind = (data.kind || "observation").toLowerCase();

      if (typeFilters[rawKind] === false) {
        res.hidden = true;
        return res;
      }

      if (searchQuery.trim().length > 0) {
        const q = searchQuery.toLowerCase();
        const matches =
          (data.label && data.label.toLowerCase().includes(q)) ||
          node.toLowerCase().includes(q);
        if (!matches) {
          res.color = "#334155";
          res.label = "";
        }
      }

      const activeId = hoveredNodeId || selectedNodeId;
      if (activeId) {
        if (node === activeId) {
          res.highlighted = true;
          res.size = (data.size || 10) * 1.35;
          res.zIndex = 10;
        } else if (graph.areNeighbors(node, activeId)) {
          res.highlighted = true;
          res.size = (data.size || 10) * 1.15;
          res.zIndex = 5;
        } else {
          res.color = "#1e293b";
          res.label = "";
          res.zIndex = 0;
        }
      }

      return res;
    });

    // Custom Edge Reducer
    renderer.setSetting("edgeReducer", (edge, data) => {
      const res = { ...data };
      const activeId = hoveredNodeId || selectedNodeId;
      if (activeId) {
        const extremities = graph.extremities(edge);
        if (extremities.includes(activeId)) {
          res.size = (data.size || 2) * 2;
          res.zIndex = 10;
        } else {
          res.color = "#0f172a";
          res.hidden = true;
          res.zIndex = 0;
        }
      }
      return res;
    });

    // Event handlers
    renderer.on("enterNode", ({ node }) => {
      setHoveredNodeId(node);
    });

    renderer.on("leaveNode", () => {
      setHoveredNodeId(null);
    });

    renderer.on("clickNode", ({ node }) => {
      setSelectedNodeId(node);
    });

    renderer.on("clickStage", () => {
      setSelectedNodeId(null);
    });

    return () => {
      renderer.kill();
      sigmaRef.current = null;
    };
  }, [rawSubgraph, normalizeId]);

  // Refresh Sigma on filter changes
  useEffect(() => {
    if (sigmaRef.current) {
      sigmaRef.current.refresh();
    }
  }, [hoveredNodeId, selectedNodeId, searchQuery, typeFilters]);

  // Selected Node Details
  const selectedNodeData = useMemo(() => {
    if (!selectedNodeId || !rawSubgraph?.nodes) return null;
    const norm = normalizeId(selectedNodeId);
    return rawSubgraph.nodes.find((n) => normalizeId(n.id) === norm || n.id === selectedNodeId) || null;
  }, [selectedNodeId, rawSubgraph, normalizeId]);

  // Connected Neighbors
  const connectedEdges = useMemo(() => {
    if (!selectedNodeId || !rawSubgraph?.edges) return [];
    const norm = normalizeId(selectedNodeId);
    return rawSubgraph.edges.filter(
      (l) => normalizeId(l.source) === norm || normalizeId(l.target) === norm,
    );
  }, [selectedNodeId, rawSubgraph, normalizeId]);

  // Camera Controls
  const handleZoomIn = () => {
    if (sigmaRef.current) {
      sigmaRef.current.getCamera().animatedZoom({ factor: 1.5 });
    }
  };

  const handleZoomOut = () => {
    if (sigmaRef.current) {
      sigmaRef.current.getCamera().animatedUnzoom({ factor: 1.5 });
    }
  };

  const handleResetCamera = () => {
    if (sigmaRef.current) {
      sigmaRef.current.getCamera().animatedReset();
    }
  };

  const handleRelayout = () => {
    if (graphRef.current && graphRef.current.order > 1) {
      try {
        forceAtlas2.assign(graphRef.current, {
          iterations: 150,
          settings: {
            gravity: 1.5,
            scalingRatio: 10,
            slowDown: 2,
            barnesHutOptimize: true,
          },
        });
        if (sigmaRef.current) {
          sigmaRef.current.refresh();
          sigmaRef.current.getCamera().animatedReset();
        }
      } catch {}
    }
  };

  const handleOpenAnalytics = async () => {
    if (!client) return;
    setIsAnalyticsOpen(true);
    setAnalyticsLoading(true);
    try {
      const report = await client.analytics(selectedProject === "all" ? undefined : selectedProject);
      setAnalyticsReport(report);
    } catch (err: any) {
      alert("Error al obtener analítica: " + err.message);
    } finally {
      setAnalyticsLoading(false);
    }
  };

  const handleOpenBlastRadius = async (nodeId?: string) => {
    if (!client) return;
    const target = nodeId || selectedNodeId;
    if (!target) {
      alert("Selecciona un nodo primero para calcular su radio de impacto");
      return;
    }
    setIsBlastOpen(true);
    setBlastLoading(true);
    try {
      const res = await client.blastRadius(target, 3);
      setBlastData(res);
    } catch (err: any) {
      alert("Error al calcular blast radius: " + err.message);
    } finally {
      setBlastLoading(false);
    }
  };


  const handleConnectSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !selectedNodeId || !targetObsId) return;
    setIsConnecting(true);
    try {
      await client.createEdge({
        from_id: selectedNodeId,
        to_id: targetObsId,
        relation_type: relationType,
        weight: 0.9,
        confidence: 0.95,
        reasoning: relationReason || "Enlace creado en el explorador Sigma.js",
      });
      setIsConnectModalOpen(false);
      setTargetObsId("");
      setRelationReason("");
      loadProjectGraph(selectedProject);
    } catch (err: any) {
      alert("Error al conectar nodos: " + err.message);
    } finally {
      setIsConnecting(false);
    }
  };

  const handleResolveSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !selectedNodeId || !obsoleteObsId) return;
    setIsResolving(true);
    try {
      await client.resolveConflict({
        new_observation_id: selectedNodeId,
        obsolete_observation_id: obsoleteObsId,
        reason: resolveReason || "Resolución de conflicto desde el visualizador Sigma.js",
      });
      setIsResolveModalOpen(false);
      setObsoleteObsId("");
      setResolveReason("");
      loadProjectGraph(selectedProject);
    } catch (err: any) {
      alert("Error al resolver conflicto: " + err.message);
    } finally {
      setIsResolving(false);
    }
  };

  return (
    <div className="flex flex-col h-[calc(100vh-4.5rem)] space-y-3">
      {/* Top Header & Controls */}
      <div className="flex flex-wrap items-center justify-between gap-3 p-3.5 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] shadow-lg shrink-0">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2">
            <Network className="h-5 w-5 text-blue-500" />
            <h1 className="text-sm sm:text-base font-bold text-[var(--text-primary)]">
              Grafo de Conocimiento Cortex
            </h1>
            <Badge variant="purple" className="text-[10px] uppercase font-mono">
              Sigma.js WebGL
            </Badge>
          </div>

          {/* Project Selector */}
          <div className="flex items-center gap-2">
            <span className="text-xs text-[var(--text-muted)] font-medium">Proyecto:</span>
            <Select
              value={selectedProject}
              onChange={(e) => setSelectedProject(e.target.value)}
              className="h-8 text-xs font-mono min-w-[130px]"
            >
              <option value="all">🌐 Todos los Proyectos</option>
              {projects.map((p) => (
                <option key={p} value={p}>
                  📁 {p}
                </option>
              ))}
            </Select>
          </div>

          {/* Search Node */}
          <div className="relative min-w-[180px] sm:min-w-[240px]">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-[var(--text-muted)]" />
            <Input
              type="text"
              placeholder="Buscar nodos o conceptos..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-8 pl-8 pr-7 text-xs"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => setSearchQuery("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-white"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        </div>

        {/* Global Action Buttons */}
        <div className="flex items-center gap-2">
          <Button
            onClick={handleRelayout}
            variant="outline"
            size="sm"
            className="h-8 text-xs gap-1.5"
            title="Recalcular distribución orgánica con ForceAtlas2"
          >
            <Sparkles className="h-3.5 w-3.5 text-amber-400" />
            <span className="hidden sm:inline">Reorganizar</span>
          </Button>

          <Button
            onClick={handleOpenAnalytics}
            variant="outline"
            size="sm"
            className="h-8 text-xs gap-1.5"
          >
            <Activity className="h-3.5 w-3.5 text-emerald-400" />
            <span className="hidden sm:inline">Analítica</span>
          </Button>

          <Button
            onClick={() => handleOpenBlastRadius()}
            variant="outline"
            size="sm"
            className="h-8 text-xs gap-1.5 text-rose-400 border-rose-900/50 hover:bg-rose-950/30"
          >
            <Flame className="h-3.5 w-3.5" />
            <span className="hidden sm:inline">Blast Radius</span>
          </Button>
        </div>
      </div>

      {/* Filter Bar */}
      <div className="flex flex-wrap items-center justify-between gap-2 px-3 py-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-xs shrink-0">
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider mr-1 flex items-center gap-1">
            <Filter className="h-3 w-3" /> Filtrar Tipos:
          </span>
          {Object.entries(KIND_COLORS).map(([kind, info]) => {
            const isActive = typeFilters[kind] !== false;
            return (
              <button
                key={kind}
                type="button"
                onClick={() =>
                  setTypeFilters((prev) => ({ ...prev, [kind]: !isActive }))
                }
                className={`px-2.5 py-1 rounded-full text-[10px] font-mono border transition-all flex items-center gap-1.5 ${
                  isActive
                    ? "bg-slate-800 border-slate-600 text-white shadow-sm"
                    : "opacity-40 bg-transparent border-transparent text-slate-500 hover:opacity-75"
                }`}
              >
                <span
                  className="w-2 h-2 rounded-full"
                  style={{ backgroundColor: info.hex }}
                />
                <span className="capitalize">{kind}</span>
              </button>
            );
          })}
        </div>

        <div className="text-[11px] text-[var(--text-muted)] font-mono">
          Nodos: <b className="text-white">{rawSubgraph?.nodes?.length || 0}</b> | Relaciones:{" "}
          <b className="text-white">{rawSubgraph?.edges?.length || 0}</b>
        </div>
      </div>

      {/* Main Canvas & Details Drawer Container */}
      <div className="relative flex-1 w-full rounded-xl bg-slate-950 border border-[var(--border-subtle)] overflow-hidden shadow-2xl">
        {/* Sigma WebGL Canvas Container */}
        <div ref={containerRef} className="w-full h-full cursor-grab active:cursor-grabbing" />

        {/* Loading Spinner Overlay */}
        {loading && (
          <div className="absolute inset-0 bg-slate-950/70 backdrop-blur-sm flex items-center justify-center z-20">
            <div className="flex flex-col items-center gap-3 p-4 rounded-xl bg-slate-900 border border-slate-800 shadow-2xl">
              <span className="h-6 w-6 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
              <span className="text-xs font-mono text-slate-300">
                Construyendo Grafo WebGL en Sigma.js...
              </span>
            </div>
          </div>
        )}

        {/* Error Alert */}
        {error && (
          <div className="absolute top-4 left-4 right-4 p-3 rounded-lg bg-rose-950/80 border border-rose-800 text-rose-200 text-xs flex items-center justify-between z-20">
            <span>{error}</span>
            <Button size="sm" variant="ghost" onClick={() => loadProjectGraph(selectedProject)}>
              Reintentar
            </Button>
          </div>
        )}

        {/* Floating Zoom & Camera Controls */}
        <div className="absolute bottom-4 left-4 flex items-center gap-1.5 p-1.5 rounded-xl bg-slate-900/90 border border-slate-800 shadow-xl backdrop-blur-md z-10">
          <Button
            size="sm"
            variant="ghost"
            onClick={handleZoomIn}
            className="h-8 w-8 p-0 text-slate-300 hover:text-white"
            title="Zoom In"
          >
            <ZoomIn className="h-4 w-4" />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={handleZoomOut}
            className="h-8 w-8 p-0 text-slate-300 hover:text-white"
            title="Zoom Out"
          >
            <ZoomOut className="h-4 w-4" />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={handleResetCamera}
            className="h-8 w-8 p-0 text-slate-300 hover:text-white"
            title="Centrar Grafo"
          >
            <RotateCcw className="h-4 w-4" />
          </Button>
        </div>

        {/* Floating Legend */}
        <div className="absolute bottom-4 right-4 hidden md:flex flex-col gap-1 p-2.5 rounded-xl bg-slate-900/90 border border-slate-800 shadow-xl backdrop-blur-md text-[10px] font-mono text-slate-300 z-10">
          <span className="font-bold text-slate-400 uppercase tracking-wider mb-1">Relaciones:</span>
          <div className="flex items-center gap-2">
            <span className="w-2.5 h-0.5 bg-purple-500 rounded" />
            <span>relates_to</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-2.5 h-0.5 bg-emerald-500 rounded" />
            <span>supersedes</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-2.5 h-0.5 bg-rose-500 rounded" />
            <span>contradicts</span>
          </div>
          <div className="flex items-center gap-2">
            <span className="w-2.5 h-0.5 bg-amber-500 rounded" />
            <span>follows</span>
          </div>
        </div>

        {/* Selected Node Details Drawer */}
        {selectedNodeData && (
          <div className="absolute top-4 right-4 w-80 sm:w-96 max-h-[calc(100%-2rem)] flex flex-col p-4 rounded-xl bg-slate-900/95 border border-slate-800 shadow-2xl backdrop-blur-md overflow-y-auto text-xs space-y-3.5 z-20 animate-in fade-in slide-in-from-right-4 duration-200">
            <div className="flex items-start justify-between gap-2 pb-2 border-b border-slate-800">
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  <Badge
                    variant={
                      KIND_COLORS[selectedNodeData.kind?.toLowerCase() || "observation"]?.variant || "default"
                    }
                    className="text-[10px] font-mono capitalize"
                  >
                    {selectedNodeData.kind || "Observation"}
                  </Badge>
                  <span className="text-[10px] font-mono text-slate-400">
                    ID: {selectedNodeData.id?.slice(0, 8)}...
                  </span>
                </div>
                <h3 className="font-bold text-sm text-white leading-tight">
                  {selectedNodeData.label || selectedNodeData.id}
                </h3>
              </div>
              <button
                type="button"
                onClick={() => setSelectedNodeId(null)}
                className="text-slate-400 hover:text-white p-1 rounded-lg hover:bg-slate-800"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* Node Metadata */}
            <div className="grid grid-cols-2 gap-2 text-[11px] font-mono">
              <div className="p-2 rounded-lg bg-slate-800/60 border border-slate-700/50">
                <span className="text-slate-400 block text-[9px] uppercase">PROYECTO</span>
                <span className="text-slate-200 font-semibold">{selectedNodeData.project || "default"}</span>
              </div>
              <div className="p-2 rounded-lg bg-slate-800/60 border border-slate-700/50">
                <span className="text-slate-400 block text-[9px] uppercase">SCORE</span>
                <span className="text-emerald-400 font-semibold">
                  {((typeof selectedNodeData.metadata?.importance_score === "number" ? selectedNodeData.metadata.importance_score : 0.5)).toFixed(2)}
                </span>
              </div>
            </div>

            {/* Content Preview */}
            {Boolean(selectedNodeData.metadata?.content) && (
              <div className="space-y-1">
                <span className="text-[10px] font-bold text-slate-400 uppercase tracking-wider block">
                  CONTENIDO / DECISIÓN
                </span>
                <div className="p-2.5 rounded-lg bg-black/40 border border-slate-800 text-[11px] font-mono text-slate-300 leading-relaxed max-h-40 overflow-y-auto whitespace-pre-wrap">
                  {String(selectedNodeData.metadata?.content || "")}
                </div>
              </div>
            )}

            {/* Connected Relations List */}
            <div className="space-y-1.5">
              <span className="text-[10px] font-bold text-slate-400 uppercase tracking-wider block">
                RELACIONES CONECTADAS ({connectedEdges.length})
              </span>
              {connectedEdges.length === 0 ? (
                <p className="text-[11px] text-slate-500 italic">Nodo aislado sin conexiones directas.</p>
              ) : (
                <div className="space-y-1 max-h-36 overflow-y-auto">
                  {connectedEdges.map((edge, idx) => {
                    const isSource = normalizeId(edge.source) === normalizeId(selectedNodeData.id);
                    const neighborId = isSource ? edge.target : edge.source;
                    return (
                      <div
                        key={idx}
                        className="p-1.5 rounded-lg bg-slate-800/40 border border-slate-800 flex items-center justify-between text-[11px] font-mono"
                      >
                        <span className="flex items-center gap-1.5 text-slate-300">
                          <span
                            className="w-2 h-2 rounded-full"
                            style={{
                              backgroundColor:
                                RELATION_COLORS[edge.type?.toLowerCase() || "relates_to"] || DEFAULT_RELATION_COLOR,
                            }}
                          />
                          <span className="text-slate-400">{edge.type || "relates_to"}</span>
                          <span>{isSource ? "→" : "←"}</span>
                          <span className="text-blue-300">{neighborId?.slice(0, 8)}...</span>
                        </span>
                        <span className="text-[10px] text-slate-500">{(edge.weight || 1).toFixed(1)}</span>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>

            {/* Actions for Selected Node */}
            <div className="pt-2 border-t border-slate-800 flex flex-col gap-2">
              <Button
                onClick={() => setIsConnectModalOpen(true)}
                size="sm"
                className="w-full text-xs gap-1.5 bg-blue-600 hover:bg-blue-500 text-white"
              >
                <LinkIcon className="h-3.5 w-3.5" />
                <span>Crear Relación Semántica</span>
              </Button>

              <div className="grid grid-cols-2 gap-2">
                <Button
                  onClick={() => setIsResolveModalOpen(true)}
                  size="sm"
                  variant="outline"
                  className="text-xs gap-1.5 text-amber-400 border-amber-900/50 hover:bg-amber-950/30"
                >
                  <ShieldAlert className="h-3.5 w-3.5" />
                  <span>Superar Decisión</span>
                </Button>

                <Button
                  onClick={() => handleOpenBlastRadius(selectedNodeData.id)}
                  size="sm"
                  variant="outline"
                  className="text-xs gap-1.5 text-rose-400 border-rose-900/50 hover:bg-rose-950/30"
                >
                  <Flame className="h-3.5 w-3.5" />
                  <span>Blast Radius</span>
                </Button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Connect Nodes Modal */}
      <Dialog open={isConnectModalOpen} onOpenChange={setIsConnectModalOpen}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm">
            <LinkIcon className="h-4 w-4 text-blue-400" />
            <span>Crear Relación en el Grafo de Conocimiento</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsConnectModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleConnectSubmit} className="space-y-4 text-xs mt-2">
          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">NODO ORIGEN</label>
            <Input value={selectedNodeId || ""} disabled className="font-mono h-9 bg-slate-800/50" />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">NODO DESTINO</label>
            <Select
              value={targetObsId}
              onChange={(e) => setTargetObsId(e.target.value)}
              className="h-9 w-full text-xs font-mono"
              required
            >
              <option value="">Selecciona observación destino...</option>
              {observations
                .filter((o) => o.id !== selectedNodeId)
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    [{o.type}] {o.title || o.content?.slice(0, 50)}... ({o.id.slice(0, 8)})
                  </option>
                ))}
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">TIPO DE RELACIÓN</label>
            <Select
              value={relationType}
              onChange={(e) => setRelationType(e.target.value)}
              className="h-9 w-full text-xs"
            >
              <option value="relates_to">relates_to (Relación Semántica Directa)</option>
              <option value="supersedes">supersedes (Reemplaza / Deja Obsoleto)</option>
              <option value="contradicts">contradicts (Contradicción Lógica)</option>
              <option value="follows">follows (Secuencia Temporal / Causal)</option>
              <option value="references">references (Referencia Externa)</option>
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">RAZONAMIENTO (OPCIONAL)</label>
            <Input
              value={relationReason}
              onChange={(e) => setRelationReason(e.target.value)}
              placeholder="Justificación del enlace semántico..."
              className="h-9 text-xs"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsConnectModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isConnecting || !targetObsId}>
              {isConnecting ? "Conectando..." : "Crear Relación"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Resolve Conflict Modal */}
      <Dialog open={isResolveModalOpen} onOpenChange={setIsResolveModalOpen}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm">
            <ShieldAlert className="h-4 w-4 text-amber-400" />
            <span>Superar Decisión Obsoleta (Resolución de Conflicto)</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsResolveModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleResolveSubmit} className="space-y-4 text-xs mt-2">
          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">NUEVA DECISIÓN ACTIVA</label>
            <Input value={selectedNodeId || ""} disabled className="font-mono h-9 bg-slate-800/50" />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">DECISIÓN OBSOLETA A SUPERAR</label>
            <Select
              value={obsoleteObsId}
              onChange={(e) => setObsoleteObsId(e.target.value)}
              className="h-9 w-full text-xs font-mono"
              required
            >
              <option value="">Selecciona decisión que queda obsoleta...</option>
              {observations
                .filter((o) => o.id !== selectedNodeId)
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    [{o.type}] {o.title || o.content?.slice(0, 50)}... ({o.id.slice(0, 8)})
                  </option>
                ))}
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-400 block uppercase">MOTIVO DEL CAMBIO ARQUITECTÓNICO</label>
            <Input
              value={resolveReason}
              onChange={(e) => setResolveReason(e.target.value)}
              placeholder="Ej: Migración de infraestructura a Railway aprobada en RFC"
              className="h-9 text-xs"
              required
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsResolveModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isResolving || !obsoleteObsId}>
              {isResolving ? "Aplicando..." : "Superar y Archivar"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Analytics Dialog */}
      <Dialog open={isAnalyticsOpen} onOpenChange={setIsAnalyticsOpen}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm">
            <Activity className="h-4 w-4 text-emerald-400" />
            <span>Métricas & Analítica Estructural del Grafo</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsAnalyticsOpen(false)} />
        </DialogHeader>

        {analyticsLoading ? (
          <div className="py-8 flex justify-center text-slate-400 text-xs">
            <span className="h-5 w-5 animate-spin rounded-full border-2 border-emerald-500 border-t-transparent mr-2" />
            Calculando métricas de centralidad y modularidad...
          </div>
        ) : analyticsReport ? (
          <div className="space-y-4 text-xs mt-2">
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5 font-mono">
              <div className="p-3 rounded-xl bg-slate-800/50 border border-slate-700/50">
                <span className="text-[10px] text-slate-400 uppercase block">NODOS</span>
                <span className="text-base font-bold text-white">{analyticsReport.total_nodes}</span>
              </div>
              <div className="p-3 rounded-xl bg-slate-800/50 border border-slate-700/50">
                <span className="text-[10px] text-slate-400 uppercase block">ARISTAS</span>
                <span className="text-base font-bold text-white">{analyticsReport.total_edges}</span>
              </div>
              <div className="p-3 rounded-xl bg-slate-800/50 border border-slate-700/50">
                <span className="text-[10px] text-slate-400 uppercase block">DENSIDAD</span>
                <span className="text-base font-bold text-emerald-400">
                  {(analyticsReport.density || 0).toFixed(3)}
                </span>
              </div>
              <div className="p-3 rounded-xl bg-slate-800/50 border border-slate-700/50">
                <span className="text-[10px] text-slate-400 uppercase block">COMUNIDADES</span>
                <span className="text-base font-bold text-purple-400">
                  {analyticsReport.communities?.length || 0}
                </span>
              </div>
            </div>

            {analyticsReport.god_nodes && analyticsReport.god_nodes.length > 0 && (
              <div className="space-y-1.5">
                <span className="text-[11px] font-bold text-slate-400 uppercase tracking-wider block">
                  Nodos Centrales (Hubs de Arquitectura):
                </span>
                <div className="space-y-1 max-h-48 overflow-y-auto">
                  {analyticsReport.god_nodes.map((node, idx) => (
                    <div
                      key={idx}
                      className="p-2 rounded-lg bg-slate-800/40 border border-slate-800 flex items-center justify-between font-mono text-[11px]"
                    >
                      <span className="text-slate-200 truncate max-w-[240px]">
                        {node.label || node.id}
                      </span>
                      <span className="text-emerald-400">
                        Degree: {node.degree} (In: {node.in_degree}, Out: {node.out_degree})
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        ) : (
          <p className="text-xs text-slate-500 py-4">No hay datos de analítica disponibles.</p>
        )}
      </Dialog>

      {/* Blast Radius Dialog */}
      <Dialog open={isBlastOpen} onOpenChange={setIsBlastOpen}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2 text-sm">
            <Flame className="h-4 w-4 text-rose-400" />
            <span>Análisis de Blast Radius (Impacto de Modificación)</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsBlastOpen(false)} />
        </DialogHeader>

        {blastLoading ? (
          <div className="py-8 flex justify-center text-slate-400 text-xs">
            <span className="h-5 w-5 animate-spin rounded-full border-2 border-rose-500 border-t-transparent mr-2" />
            Calculando ondas de impacto y dependencias en cascada...
          </div>
        ) : blastData ? (
          <div className="space-y-4 text-xs mt-2">
            <div className="p-3 rounded-xl bg-rose-950/30 border border-rose-900/40 text-rose-200">
              <span className="font-semibold block">
                Impacto Calculado: {(blastData.total_impacted || []).length} nodos afectados ({((blastData.blast_radius_pct || 0) * 100).toFixed(1)}% del grafo).
              </span>
            </div>

            {blastData.direct_impact && blastData.direct_impact.length > 0 && (
              <div className="space-y-1.5">
                <span className="text-[11px] font-bold text-slate-400 uppercase tracking-wider block">
                  Impacto Directo (1 Salto):
                </span>
                <div className="space-y-1 max-h-32 overflow-y-auto">
                  {blastData.direct_impact.map((nodeId, idx) => (
                    <div
                      key={idx}
                      className="p-2 rounded-lg bg-slate-800/40 border border-slate-800 flex items-center justify-between font-mono text-[11px]"
                    >
                      <span className="text-rose-300 truncate max-w-[280px]">
                        {nodeId}
                      </span>
                      <span className="text-rose-400 text-[10px]">
                        Directo
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {blastData.total_impacted && blastData.total_impacted.length > 0 && (
              <div className="space-y-1.5">
                <span className="text-[11px] font-bold text-slate-400 uppercase tracking-wider block">
                  Impacto Total en Cascada ({blastData.total_impacted.length}):
                </span>
                <div className="space-y-1 max-h-40 overflow-y-auto">
                  {blastData.total_impacted.map((nodeId, idx) => (
                    <div
                      key={idx}
                      className="p-1.5 rounded-lg bg-slate-800/30 border border-slate-800/50 flex items-center justify-between font-mono text-[11px]"
                    >
                      <span className="text-slate-300 truncate max-w-[280px]">
                        {nodeId}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        ) : (
          <p className="text-xs text-slate-500 py-4">No hay datos de blast radius.</p>
        )}
      </Dialog>
    </div>
  );
}

export default GraphPageContent;
