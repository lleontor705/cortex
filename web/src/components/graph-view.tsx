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
  Brain,
  Code2,
  Globe2,
  FileDown,
  BookOpen,
  FolderGit2,
  FileCode,
  Compass,
} from "lucide-react";

import Graph from "graphology";
import Sigma from "sigma";
import forceAtlas2 from "graphology-layout-forceatlas2";
import { circular } from "graphology-layout";
import JSZip from "jszip";

const RELATION_COLORS: Record<string, string> = {
  references: "#3b82f6",
  supersedes: "#10b981",
  contradicts: "#ef4444",
  follows: "#f59e0b",
  relates_to: "#8b5cf6",
  caused_by: "#ec4899",
  calls: "#06b6d4",
  imports: "#84cc16",
  implements: "#d946ef",
  defines: "#6366f1",
  uses: "#3b82f6",
};

const DEFAULT_RELATION_COLOR = "#64748b";

const KIND_COLORS: Record<
  string,
  {
    bg: string;
    border: string;
    text: string;
    hex: string;
    isCode?: boolean;
    variant: "default" | "destructive" | "success" | "warning" | "purple" | "secondary";
  }
> = {
  decision: { bg: "#1e3a8a", border: "#3b82f6", text: "#93c5fd", hex: "#3b82f6", variant: "default" },
  bugfix: { bg: "#7f1d1d", border: "#ef4444", text: "#fca5a5", hex: "#ef4444", variant: "destructive" },
  pattern: { bg: "#064e3b", border: "#10b981", text: "#6ee7b7", hex: "#10b981", variant: "success" },
  discovery: { bg: "#78350f", border: "#f59e0b", text: "#fcd34d", hex: "#f59e0b", variant: "warning" },
  learning: { bg: "#4c1d95", border: "#8b5cf6", text: "#c4b5fd", hex: "#8b5cf6", variant: "purple" },
  config: { bg: "#164e63", border: "#06b6d4", text: "#a5f3fc", hex: "#06b6d4", variant: "default" },
  session: { bg: "#134e4a", border: "#14b8a6", text: "#5eead4", hex: "#14b8a6", variant: "success" },
  observation: { bg: "#1e293b", border: "#475569", text: "#cbd5e1", hex: "#64748b", variant: "secondary" },
  code_entity: { bg: "#1e1b4b", border: "#6366f1", text: "#c7d2fe", hex: "#6366f1", isCode: true, variant: "purple" },
  module: { bg: "#0f372c", border: "#059669", text: "#6ee7b7", hex: "#059669", isCode: true, variant: "success" },
  function: { bg: "#083344", border: "#0891b2", text: "#67e8f9", hex: "#0891b2", isCode: true, variant: "default" },
  class: { bg: "#361a38", border: "#a21caf", text: "#f0abfc", hex: "#a21caf", isCode: true, variant: "purple" },
  interface: { bg: "#3b1e08", border: "#d97706", text: "#fcd34d", hex: "#d97706", isCode: true, variant: "warning" },
  entity: { bg: "#282736", border: "#818cf8", text: "#e0e7ff", hex: "#818cf8", variant: "secondary" },
};

const COMMUNITY_COLORS = [
  "#3b82f6",
  "#10b981",
  "#f59e0b",
  "#8b5cf6",
  "#ec4899",
  "#06b6d4",
  "#eab308",
  "#14b8a6",
  "#f43f5e",
  "#6366f1",
  "#84cc16",
  "#d946ef",
];

function safeObsidianSlug(s: string): string {
  const norm = s
    .toLowerCase()
    .trim()
    .replace(/[<>:"/\\|?*\x00-\x1f]+/g, "-")
    .replace(/[ .-]+$/, "");
  return norm.length > 60 ? norm.slice(0, 60) : norm || "untitled";
}

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

  // Graph Layer / Separation Mode
  const [graphLayer, setGraphLayer] = useState<"knowledge" | "code" | "all">("knowledge");

  // Raw Graph & Observations State
  const [rawSubgraph, setRawSubgraph] = useState<GraphSubgraph | null>(null);
  const [observations, setObservations] = useState<Observation[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [isExportingObsidian, setIsExportingObsidian] = useState(false);

  // Selection & Hover
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [hoveredNodeId, setHoveredNodeId] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState("");

  // Color Mode
  const [colorMode, setColorMode] = useState<"kind" | "community">("kind");

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
    code_entity: true,
    module: true,
    function: true,
    class: true,
    interface: true,
    entity: true,
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
        const data: GraphSubgraph = await client.projectGraph(project === "all" ? undefined : project, 400);
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
      .listObservations("?limit=300")
      .then((res) => {
        setObservations(res || []);
      })
      .catch(() => {});
  }, [client]);

  useEffect(() => {
    loadProjectGraph(selectedProject);
  }, [selectedProject, loadProjectGraph]);

  // Filter nodes based on selected Layer
  const isNodeInSelectedLayer = useCallback(
    (kind: string) => {
      const isCode =
        kind === "code_entity" ||
        kind === "module" ||
        kind === "function" ||
        kind === "class" ||
        kind === "interface";
      if (graphLayer === "knowledge") return !isCode;
      if (graphLayer === "code") return isCode;
      return true;
    },
    [graphLayer],
  );

  // Calculate Layer statistics
  const layerStats = useMemo(() => {
    if (!rawSubgraph?.nodes) return { knowledge: 0, code: 0, total: 0 };
    let knowledge = 0;
    let code = 0;
    rawSubgraph.nodes.forEach((n) => {
      const k = (n.kind || "observation").toLowerCase();
      if (
        k === "code_entity" ||
        k === "module" ||
        k === "function" ||
        k === "class" ||
        k === "interface"
      ) {
        code++;
      } else {
        knowledge++;
      }
    });
    return { knowledge, code, total: rawSubgraph.nodes.length };
  }, [rawSubgraph]);

  // Build Sigma Graphology Instance
  useEffect(() => {
    if (!containerRef.current || !rawSubgraph) return;

    if (sigmaRef.current) {
      sigmaRef.current.kill();
      sigmaRef.current = null;
    }

    const graph = new Graph();
    graphRef.current = graph;

    const allNodes = rawSubgraph.nodes || [];
    const allEdges = rawSubgraph.edges || [];

    // Filter nodes according to current Layer
    const visibleNodes = allNodes.filter((n) => {
      const kind = (n.kind || "observation").toLowerCase();
      return isNodeInSelectedLayer(kind);
    });

    const visibleNodeIds = new Set(visibleNodes.map((n) => normalizeId(n.id) || n.id));

    // Add Nodes to Graphology
    visibleNodes.forEach((n, idx) => {
      const id = normalizeId(n.id) || n.id;
      if (graph.hasNode(id)) return;

      const kind = (n.kind || "observation").toLowerCase();
      const kindInfo = KIND_COLORS[kind] || KIND_COLORS.observation;
      const score = typeof n.metadata?.importance_score === "number" ? n.metadata.importance_score : 0.5;
      const size = Math.max(7, Math.min(24, 9 + score * 13));

      // Initial circular seed placement for ForceAtlas2
      const angle = (idx / Math.max(1, visibleNodes.length)) * 2 * Math.PI;
      const radius = 100 + (idx % 8) * 35;
      const x = Math.cos(angle) * radius + (Math.random() - 0.5) * 20;
      const y = Math.sin(angle) * radius + (Math.random() - 0.5) * 20;

      // Color selection (kind vs community)
      let nodeColor = kindInfo.hex;
      if (colorMode === "community" && typeof n.metadata?.community === "number") {
        const commIdx = n.metadata.community % COMMUNITY_COLORS.length;
        nodeColor = COMMUNITY_COLORS[commIdx];
      }

      graph.addNode(id, {
        x,
        y,
        size,
        label: n.label || id.slice(0, 10),
        color: nodeColor,
        kind,
        raw: n,
      });
    });

    // Add Edges connecting visible nodes
    allEdges.forEach((l, idx) => {
      const source = normalizeId(l.source);
      const target = normalizeId(l.target);

      if (visibleNodeIds.has(source) && visibleNodeIds.has(target)) {
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
            scalingRatio: 9,
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
      minCameraRatio: 0.08,
      maxCameraRatio: 12,
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
          res.size = (data.size || 2) * 2.2;
          res.zIndex = 10;
        } else {
          res.color = "#0f172a";
          res.hidden = true;
          res.zIndex = 0;
        }
      }
      return res;
    });

    // Event listeners
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
  }, [rawSubgraph, graphLayer, colorMode, isNodeInSelectedLayer, normalizeId]);

  // Refresh Sigma on state updates
  useEffect(() => {
    if (sigmaRef.current) {
      sigmaRef.current.refresh();
    }
  }, [hoveredNodeId, selectedNodeId, searchQuery, typeFilters, colorMode]);

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

  // Export to Obsidian Vault (.zip)
  const handleExportObsidian = async () => {
    if (!rawSubgraph || !client) return;
    setIsExportingObsidian(true);
    try {
      const zip = new JSZip();
      const projectDir = `cortex/projects/${safeObsidianSlug(selectedProject)}`;

      // Map edges to build WikiLinks for each observation
      const edgeMap = new Map<string, Array<{ targetId: string; type: string }>>();
      (rawSubgraph.edges || []).forEach((e) => {
        const src = normalizeId(e.source);
        const tgt = normalizeId(e.target);
        if (!edgeMap.has(src)) edgeMap.set(src, []);
        edgeMap.get(src)!.push({ targetId: tgt, type: e.type });

        if (!edgeMap.has(tgt)) edgeMap.set(tgt, []);
        edgeMap.get(tgt)!.push({ targetId: src, type: e.type });
      });

      // Build node lookup map
      const nodeMap = new Map<string, GraphNode>();
      (rawSubgraph.nodes || []).forEach((n) => {
        nodeMap.set(normalizeId(n.id) || n.id, n);
      });

      // Generate Markdown note for each node in the project
      (rawSubgraph.nodes || []).forEach((node) => {
        const id = normalizeId(node.id) || node.id;
        const kind = (node.kind || "observation").toLowerCase();
        const title = node.label || `Observation ${id}`;
        const slug = safeObsidianSlug(title);
        const filename = `${slug}-${id}.md`;

        // Frontmatter
        let content = `---\n`;
        content += `cortex_id: ${id}\n`;
        content += `title: "${title.replace(/"/g, '\\"')}"\n`;
        content += `type: ${kind}\n`;
        content += `project: ${selectedProject}\n`;
        content += `created_at: ${node.metadata?.created_at || new Date().toISOString()}\n`;
        content += `tags:\n`;
        content += `  - cortex\n`;
        content += `  - ${kind}\n`;
        content += `  - ${safeObsidianSlug(selectedProject)}\n`;
        content += `---\n\n`;

        // Body
        content += `# ${title}\n\n`;
        if (node.metadata?.content) {
          content += `${node.metadata.content}\n\n`;
        } else {
          content += `*Observación registrada en el Grafo de Conocimiento Cortex.*\n\n`;
        }

        // Related WikiLinks
        const neighbors = edgeMap.get(id) || [];
        if (neighbors.length > 0) {
          content += `## Related\n\n`;
          const uniqueLinks = new Set<string>();
          neighbors.forEach(({ targetId, type }) => {
            const targetNode = nodeMap.get(targetId);
            if (targetNode) {
              const targetTitle = targetNode.label || `Observation ${targetId}`;
              const targetSlug = safeObsidianSlug(targetTitle);
              uniqueLinks.add(`- [[${targetSlug}-${targetId}]] *(${type})*`);
            }
          });
          content += Array.from(uniqueLinks).join("\n") + "\n";
        }

        zip.file(`${projectDir}/${filename}`, content);
      });

      // Manifest
      const manifest = {
        vault: "cortex",
        project: selectedProject,
        exported_at: new Date().toISOString(),
        total_notes: rawSubgraph.nodes?.length || 0,
        total_relations: rawSubgraph.edges?.length || 0,
      };
      zip.file(`${projectDir}/manifest.json`, JSON.stringify(manifest, null, 2));

      // Generate and trigger download
      const blob = await zip.generateAsync({ type: "blob" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `cortex-obsidian-${safeObsidianSlug(selectedProject)}.zip`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    } catch (err: any) {
      alert("Error al exportar la bóveda Obsidian: " + err.message);
    } finally {
      setIsExportingObsidian(false);
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
      {/* Top Header & Layer Selector */}
      <div className="flex flex-wrap items-center justify-between gap-3 p-3.5 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] shadow-lg shrink-0">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex items-center gap-2">
            <Network className="h-5 w-5 text-blue-500" />
            <h1 className="text-sm sm:text-base font-bold text-[var(--text-primary)]">
              Grafo Cortex
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

          {/* Layer Selector Tabs */}
          <div className="flex items-center p-1 bg-[var(--bg-surface)] border border-[var(--border-subtle)] rounded-lg">
            <button
              type="button"
              onClick={() => setGraphLayer("knowledge")}
              className={`flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-md transition-colors ${
                graphLayer === "knowledge"
                  ? "bg-blue-600 text-white shadow-sm"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Brain className="h-3.5 w-3.5" />
              <span>Grafo de Conocimiento</span>
              <span className="text-[10px] opacity-80 font-mono ml-0.5">({layerStats.knowledge})</span>
            </button>

            <button
              type="button"
              onClick={() => setGraphLayer("code")}
              className={`flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-md transition-colors ${
                graphLayer === "code"
                  ? "bg-indigo-600 text-white shadow-sm"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Code2 className="h-3.5 w-3.5" />
              <span>Código y Arquitectura</span>
              <span className="text-[10px] opacity-80 font-mono ml-0.5">({layerStats.code})</span>
            </button>

            <button
              type="button"
              onClick={() => setGraphLayer("all")}
              className={`flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-md transition-colors ${
                graphLayer === "all"
                  ? "bg-purple-600 text-white shadow-sm"
                  : "text-[var(--text-muted)] hover:text-[var(--text-primary)]"
              }`}
            >
              <Globe2 className="h-3.5 w-3.5" />
              <span>Grafo Unificado</span>
              <span className="text-[10px] opacity-80 font-mono ml-0.5">({layerStats.total})</span>
            </button>
          </div>

          {/* Search Node */}
          <div className="relative min-w-[170px] sm:min-w-[210px]">
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
          {/* Obsidian Vault Export Button */}
          <Button
            onClick={handleExportObsidian}
            variant="outline"
            size="sm"
            disabled={isExportingObsidian || !rawSubgraph?.nodes?.length}
            className="h-8 text-xs gap-1.5 text-purple-400 border-purple-900/50 hover:bg-purple-950/30"
            title="Descargar todas las notas interconectadas con [[WikiLinks]] para Obsidian"
          >
            <Download className="h-3.5 w-3.5" />
            <span>{isExportingObsidian ? "Generando Vault..." : "Exportar a Obsidian (.zip)"}</span>
          </Button>

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
            onClick={() => setColorMode((prev) => (prev === "kind" ? "community" : "kind"))}
            variant="outline"
            size="sm"
            className={`h-8 text-xs gap-1.5 ${
              colorMode === "community" ? "bg-emerald-950/40 text-emerald-300 border-emerald-800" : ""
            }`}
            title="Alternar entre coloreado por tipo y por cluster/comunidad Louvain"
          >
            <Compass className="h-3.5 w-3.5 text-emerald-400" />
            <span className="hidden sm:inline">
              {colorMode === "community" ? "Comunidades Activas" : "Ver Comunidades"}
            </span>
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
          {Object.entries(KIND_COLORS)
            .filter(([k]) => isNodeInSelectedLayer(k))
            .map(([kind, info]) => {
              const isActive = typeFilters[kind] !== false;
              return (
                <button
                  key={kind}
                  type="button"
                  onClick={() =>
                    setTypeFilters((prev) => ({ ...prev, [kind]: !isActive }))
                  }
                  className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px] font-medium border transition-all ${
                    isActive
                      ? "opacity-100 shadow-sm"
                      : "opacity-40 grayscale border-transparent bg-transparent"
                  }`}
                  style={{
                    backgroundColor: isActive ? info.bg : undefined,
                    borderColor: isActive ? info.border : undefined,
                    color: isActive ? info.text : "var(--text-muted)",
                  }}
                >
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ backgroundColor: info.hex }}
                  />
                  <span>{kind}</span>
                </button>
              );
            })}
        </div>

        <div className="flex items-center gap-3 text-[11px] text-[var(--text-muted)]">
          <span>
            Nodos en Vista:{" "}
            <b className="text-[var(--text-primary)]">
              {sigmaRef.current?.getGraph()?.order || 0}
            </b>
          </span>
          <span>
            Aristas:{" "}
            <b className="text-[var(--text-primary)]">
              {sigmaRef.current?.getGraph()?.size || 0}
            </b>
          </span>
        </div>
      </div>

      {/* Main Canvas & Inspector Area */}
      <div className="relative flex-1 rounded-xl bg-[#090d16] border border-[var(--border-subtle)] overflow-hidden shadow-inner flex">
        {/* Loading Spinner */}
        {loading && (
          <div className="absolute inset-0 z-30 flex flex-col items-center justify-center bg-black/60 backdrop-blur-sm gap-2">
            <span className="h-6 w-6 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
            <span className="text-xs text-slate-300">Cargando Grafo WebGL...</span>
          </div>
        )}

        {/* Error Alert */}
        {error && (
          <div className="absolute top-4 left-4 right-4 z-30 p-3 rounded-lg bg-red-950/80 border border-red-800 text-red-300 text-xs flex items-center justify-between">
            <span>{error}</span>
            <Button size="sm" variant="outline" onClick={() => loadProjectGraph(selectedProject)}>
              Reintentar
            </Button>
          </div>
        )}

        {/* Sigma.js WebGL Container */}
        <div ref={containerRef} className="w-full h-full cursor-grab active:cursor-grabbing" />

        {/* Floating Zoom & Camera Controls */}
        <div className="absolute bottom-4 left-4 z-20 flex flex-col gap-1.5 bg-[var(--bg-secondary)]/90 backdrop-blur-md p-1.5 rounded-lg border border-[var(--border-subtle)] shadow-xl">
          <button
            type="button"
            onClick={handleZoomIn}
            className="p-1.5 hover:bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-white rounded transition-colors"
            title="Acercar (Zoom In)"
          >
            <ZoomIn className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={handleZoomOut}
            className="p-1.5 hover:bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-white rounded transition-colors"
            title="Alejar (Zoom Out)"
          >
            <ZoomOut className="h-4 w-4" />
          </button>
          <button
            type="button"
            onClick={handleResetCamera}
            className="p-1.5 hover:bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-white rounded transition-colors"
            title="Restablecer Vista Centrada"
          >
            <RotateCcw className="h-4 w-4" />
          </button>
        </div>

        {/* Relation Legend */}
        <div className="absolute bottom-4 right-4 z-20 hidden lg:flex flex-col gap-1 bg-[var(--bg-secondary)]/90 backdrop-blur-md px-3 py-2 rounded-lg border border-[var(--border-subtle)] shadow-xl text-[11px]">
          <span className="font-semibold text-[var(--text-muted)] uppercase tracking-wider mb-0.5">
            Relaciones Semánticas
          </span>
          <div className="grid grid-cols-2 gap-x-3 gap-y-1">
            {Object.entries(RELATION_COLORS).map(([rel, col]) => (
              <div key={rel} className="flex items-center gap-1.5">
                <span className="h-1.5 w-3 rounded-full" style={{ backgroundColor: col }} />
                <span className="text-slate-300 font-mono text-[10px]">{rel}</span>
              </div>
            ))}
          </div>
        </div>

        {/* Node Detail Inspector Drawer */}
        {selectedNodeData && (
          <div className="absolute top-3 right-3 bottom-3 z-30 w-80 sm:w-96 flex flex-col rounded-xl bg-[var(--bg-secondary)]/95 backdrop-blur-md border border-[var(--border-subtle)] shadow-2xl overflow-hidden animate-in slide-in-from-right duration-200">
            {/* Header */}
            <div className="flex items-center justify-between p-3.5 border-b border-[var(--border-subtle)] bg-[var(--bg-surface)]">
              <div className="flex items-center gap-2 overflow-hidden">
                <span
                  className="h-3 w-3 rounded-full shrink-0"
                  style={{
                    backgroundColor:
                      KIND_COLORS[(selectedNodeData.kind || "observation").toLowerCase()]?.hex ||
                      "#3b82f6",
                  }}
                />
                <h3 className="text-xs font-bold text-[var(--text-primary)] truncate">
                  {selectedNodeData.label || `Nodo ${selectedNodeData.id}`}
                </h3>
              </div>
              <button
                type="button"
                onClick={() => setSelectedNodeId(null)}
                className="text-[var(--text-muted)] hover:text-white p-1"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* Body Info */}
            <div className="flex-1 overflow-y-auto p-4 space-y-4 text-xs">
              {/* Type and Score Badges */}
              <div className="flex flex-wrap items-center gap-2">
                <Badge
                  variant={
                    KIND_COLORS[(selectedNodeData.kind || "observation").toLowerCase()]?.variant ||
                    "default"
                  }
                  className="capitalize font-mono text-[10px]"
                >
                  {selectedNodeData.kind}
                </Badge>
                {typeof selectedNodeData.metadata?.importance_score === "number" && (
                  <Badge variant="warning" className="text-[10px] font-mono">
                    Score: {selectedNodeData.metadata.importance_score.toFixed(2)}
                  </Badge>
                )}
                {Boolean(selectedNodeData.metadata?.project) && (
                  <Badge variant="secondary" className="text-[10px] font-mono">
                    📁 {String(selectedNodeData.metadata?.project)}
                  </Badge>
                )}
                {typeof selectedNodeData.metadata?.community === "number" && (
                  <Badge variant="success" className="text-[10px] font-mono">
                    Cluster: #{selectedNodeData.metadata.community}
                  </Badge>
                )}
              </div>

              {/* Node Content / Description */}
              <div className="space-y-1.5">
                <label className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
                  Descripción / Contenido
                </label>
                <div className="p-2.5 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-[var(--text-secondary)] leading-relaxed whitespace-pre-wrap max-h-48 overflow-y-auto font-mono text-[11px]">
                  {String(
                    selectedNodeData.metadata?.content ||
                      selectedNodeData.metadata?.description ||
                      selectedNodeData.label ||
                      "Sin descripción adicional.",
                  )}
                </div>
              </div>

              {/* Direct Connected Neighbors */}
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <label className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider">
                    Conexiones Directas ({connectedEdges.length})
                  </label>
                </div>

                {connectedEdges.length === 0 ? (
                  <p className="text-[11px] text-[var(--text-muted)] italic">
                    Nodo aislado sin aristas directas en este proyecto.
                  </p>
                ) : (
                  <div className="space-y-1.5 max-h-44 overflow-y-auto pr-1">
                    {connectedEdges.map((e, idx) => {
                      const isOutgoing = normalizeId(e.source) === normalizeId(selectedNodeData.id);
                      const otherId = isOutgoing ? normalizeId(e.target) : normalizeId(e.source);
                      const relType = (e.type || "relates_to").toLowerCase();
                      const relColor = RELATION_COLORS[relType] || DEFAULT_RELATION_COLOR;

                      return (
                        <div
                          key={idx}
                          onClick={() => setSelectedNodeId(otherId)}
                          className="flex items-center justify-between p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] hover:border-blue-500 cursor-pointer transition-colors"
                        >
                          <div className="flex items-center gap-1.5 truncate">
                            <span className="text-[10px] font-mono text-[var(--text-muted)]">
                              {isOutgoing ? "→" : "←"}
                            </span>
                            <span className="font-medium text-[var(--text-primary)] truncate">
                              {otherId}
                            </span>
                          </div>
                          <span
                            className="text-[10px] font-mono px-1.5 py-0.5 rounded shrink-0 font-semibold"
                            style={{
                              backgroundColor: `${relColor}20`,
                              color: relColor,
                              border: `1px solid ${relColor}40`,
                            }}
                          >
                            {relType}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            </div>

            {/* Footer Action Buttons */}
            <div className="p-3 border-t border-[var(--border-subtle)] bg-[var(--bg-surface)] flex flex-col gap-2">
              <div className="grid grid-cols-2 gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setIsConnectModalOpen(true)}
                  className="text-xs gap-1"
                >
                  <LinkIcon className="h-3.5 w-3.5 text-blue-400" />
                  <span>+ Conectar</span>
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => setIsResolveModalOpen(true)}
                  className="text-xs gap-1 text-amber-400 hover:text-amber-300"
                >
                  <Zap className="h-3.5 w-3.5" />
                  <span>Resolver</span>
                </Button>
              </div>

              <Button
                size="sm"
                variant="outline"
                onClick={() => handleOpenBlastRadius(selectedNodeData.id)}
                className="w-full text-xs gap-1 text-rose-400 hover:text-rose-300"
              >
                <Flame className="h-3.5 w-3.5" />
                <span>Analizar Impacto (Blast Radius)</span>
              </Button>
            </div>
          </div>
        )}
      </div>

      {/* Modal: Graph Analytics Diagnostics */}
      <Dialog open={isAnalyticsOpen} onOpenChange={setIsAnalyticsOpen}>
        <DialogHeader>
          <DialogTitle>
            <Activity className="h-4 w-4 text-emerald-400" />
            Diagnóstico y Métricas del Grafo
          </DialogTitle>
          <DialogClose onClick={() => setIsAnalyticsOpen(false)} />
        </DialogHeader>

        <div className="space-y-4 mt-3 text-xs">
          {analyticsLoading ? (
            <div className="flex items-center justify-center py-8 gap-2 text-slate-400">
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-emerald-500 border-t-transparent" />
              <span>Calculando métricas globales del grafo...</span>
            </div>
          ) : analyticsReport ? (
            <div className="space-y-4">
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5">
                <div className="p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                  <span className="text-[10px] font-semibold text-[var(--text-muted)] uppercase block">
                    Total Nodos
                  </span>
                  <span className="text-lg font-bold text-white font-mono">
                    {analyticsReport.total_nodes}
                  </span>
                </div>
                <div className="p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                  <span className="text-[10px] font-semibold text-[var(--text-muted)] uppercase block">
                    Total Aristas
                  </span>
                  <span className="text-lg font-bold text-white font-mono">
                    {analyticsReport.total_edges}
                  </span>
                </div>
                <div className="p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                  <span className="text-[10px] font-semibold text-[var(--text-muted)] uppercase block">
                    Densidad
                  </span>
                  <span className="text-lg font-bold text-emerald-400 font-mono">
                    {analyticsReport.density.toFixed(4)}
                  </span>
                </div>
                <div className="p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                  <span className="text-[10px] font-semibold text-[var(--text-muted)] uppercase block">
                    Comunidades
                  </span>
                  <span className="text-lg font-bold text-blue-400 font-mono">
                    {analyticsReport.communities?.length || 0}
                  </span>
                </div>
              </div>

              {analyticsReport.god_nodes && analyticsReport.god_nodes.length > 0 && (
                <div className="space-y-1.5">
                  <label className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
                    Nodos Críticos / Centrales (God Nodes)
                  </label>
                  <div className="space-y-1 max-h-48 overflow-y-auto">
                    {analyticsReport.god_nodes.map((n, idx) => (
                      <div
                        key={idx}
                        className="flex items-center justify-between p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] font-mono text-[11px]"
                      >
                        <span className="text-slate-200 truncate">{n.label || n.id}</span>
                        <Badge variant="purple" className="text-[10px]">
                          Grado: {n.degree} (in: {n.in_degree}, out: {n.out_degree})
                        </Badge>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          ) : (
            <p className="text-slate-400">Sin datos de analítica disponibles.</p>
          )}
        </div>
      </Dialog>

      {/* Modal: Blast Radius Analysis */}
      <Dialog open={isBlastOpen} onOpenChange={setIsBlastOpen}>
        <DialogHeader>
          <DialogTitle>
            <Flame className="h-4 w-4 text-rose-500" />
            Análisis de Impacto (Blast Radius)
          </DialogTitle>
          <DialogClose onClick={() => setIsBlastOpen(false)} />
        </DialogHeader>

        <div className="space-y-4 mt-3 text-xs">
          {blastLoading ? (
            <div className="flex items-center justify-center py-8 gap-2 text-slate-400">
              <span className="h-4 w-4 animate-spin rounded-full border-2 border-rose-500 border-t-transparent" />
              <span>Calculando propagación de impacto...</span>
            </div>
          ) : blastData ? (
            <div className="space-y-3">
              <div className="p-3 rounded-lg bg-rose-950/20 border border-rose-900/40">
                <span className="text-slate-400 block mb-1">
                  Nodos afectados por cambios en <b className="text-white">{blastData.root_node}</b>:
                </span>
                <span className="text-xl font-bold text-rose-400 font-mono">
                  {blastData.total_impacted?.length || 0} nodos ({blastData.blast_radius_pct?.toFixed(1) || 0}% del proyecto)
                </span>
              </div>

              {blastData.total_impacted && blastData.total_impacted.length > 0 && (
                <div className="space-y-1.5">
                  <label className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
                    Nodos en la Cascada de Dependencias
                  </label>
                  <div className="space-y-1 max-h-48 overflow-y-auto">
                    {blastData.total_impacted.map((nodeName, idx) => (
                      <div
                        key={idx}
                        className="flex items-center justify-between p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] font-mono text-[11px]"
                      >
                        <span className="text-slate-200 truncate">{nodeName}</span>
                        <Badge
                          variant={blastData.direct_impact?.includes(nodeName) ? "destructive" : "secondary"}
                          className="text-[10px]"
                        >
                          {blastData.direct_impact?.includes(nodeName) ? "Impacto Directo" : "Impacto Indirecto"}
                        </Badge>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          ) : (
            <p className="text-slate-400">Selecciona un nodo para iniciar el cálculo.</p>
          )}
        </div>
      </Dialog>

      {/* Modal: Create Semantic Edge */}
      <Dialog open={isConnectModalOpen} onOpenChange={setIsConnectModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <LinkIcon className="h-4 w-4 text-blue-400" />
            Crear Conexión Semántica en el Grafo
          </DialogTitle>
          <DialogClose onClick={() => setIsConnectModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleConnectSubmit} className="space-y-3.5 mt-3 text-xs">
          <p className="text-slate-400">
            Crea una nueva arista semántica dirigida desde el nodo seleccionado.
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              OBSERVACIÓN DESTINO (TARGET)
            </label>
            <Select
              value={targetObsId}
              onChange={(e) => setTargetObsId(e.target.value)}
              required
              className="w-full text-xs"
            >
              <option value="">Selecciona la observación destino...</option>
              {observations
                .filter((o) => normalizeId(String(o.id)) !== normalizeId(selectedNodeId || ""))
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    #{o.id} - {o.title} ({o.project})
                  </option>
                ))}
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              TIPO DE RELACIÓN
            </label>
            <Select
              value={relationType}
              onChange={(e) => setRelationType(e.target.value)}
              required
              className="w-full text-xs"
            >
              <option value="relates_to">relates_to (Relación general)</option>
              <option value="references">references (Referencia técnica)</option>
              <option value="follows">follows (Secuencia lógica / temporal)</option>
              <option value="supersedes">supersedes (Reemplaza / actualiza)</option>
              <option value="contradicts">contradicts (Conflicto / discrepancia)</option>
              <option value="caused_by">caused_by (Origen o causa)</option>
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              JUSTIFICACIÓN / MOTIVO (OPCIONAL)
            </label>
            <Input
              type="text"
              value={relationReason}
              onChange={(e) => setRelationReason(e.target.value)}
              placeholder="Ej: Dependencia directa descubierta durante el análisis"
              className="text-xs"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsConnectModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isConnecting} className="bg-blue-600 hover:bg-blue-500 text-white">
              {isConnecting ? "Conectando..." : "Crear Conexión"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Modal: Resolve Conflict */}
      <Dialog open={isResolveModalOpen} onOpenChange={setIsResolveModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <Zap className="h-4 w-4 text-amber-400" />
            Resolución de Conflicto en el Grafo
          </DialogTitle>
          <DialogClose onClick={() => setIsResolveModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleResolveSubmit} className="space-y-3.5 mt-3 text-xs">
          <p className="text-slate-400">
            Marca el nodo seleccionado como la versión vigente que supera o invalida a una observación previa.
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              OBSERVACIÓN OBSOLETA A REEMPLAZAR
            </label>
            <Select
              value={obsoleteObsId}
              onChange={(e) => setObsoleteObsId(e.target.value)}
              required
              className="w-full text-xs"
            >
              <option value="">Selecciona la observación obsoleta...</option>
              {observations
                .filter((o) => normalizeId(String(o.id)) !== normalizeId(selectedNodeId || ""))
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    #{o.id} - {o.title} ({o.project})
                  </option>
                ))}
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              MOTIVO DE RESOLUCIÓN
            </label>
            <Input
              type="text"
              value={resolveReason}
              onChange={(e) => setResolveReason(e.target.value)}
              placeholder="Ej: Cambio de arquitectura aprobado en diseño"
              required
              className="text-xs"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsResolveModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isResolving} className="bg-amber-600 hover:bg-amber-500 text-white">
              {isResolving ? "Resolviendo..." : "Aplicar Resolución"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}

export default function GraphView() {
  return (
    <Suspense
      fallback={
        <div className="flex h-[60vh] items-center justify-center text-xs text-[var(--text-muted)]">
          <div className="flex items-center gap-2">
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
            <span>Cargando Grafo Sigma.js WebGL...</span>
          </div>
        </div>
      }
    >
      <GraphPageContent />
    </Suspense>
  );
}
