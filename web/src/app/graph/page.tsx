"use client";

import React, { useEffect, useRef, useState, useCallback, useMemo } from "react";
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
  Plus,
  AlertTriangle,
  Layers,
  ArrowRight,
  Info,
  X,
  Search,
  Maximize2,
  Flame,
  Link as LinkIcon,
  Compass,
  Copy,
  Check,
  Filter,
  Zap,
  Activity,
  Download,
  Network,
  Radio,
  FileCode,
  ShieldAlert,
} from "lucide-react";

interface SimulationNode extends GraphNode {
  x: number;
  y: number;
  vx: number;
  vy: number;
  radius: number;
  isPinned?: boolean;
}

interface SimulationLink extends GraphLink {
  sourceNode?: SimulationNode;
  targetNode?: SimulationNode;
}

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

const KIND_COLORS: Record<string, { bg: string; border: string; text: string; variant: "default" | "destructive" | "success" | "warning" | "purple" | "secondary" }> = {
  decision: { bg: "#1e3a8a", border: "#3b82f6", text: "#93c5fd", variant: "default" },
  bugfix: { bg: "#7f1d1d", border: "#ef4444", text: "#fca5a5", variant: "destructive" },
  pattern: { bg: "#064e3b", border: "#10b981", text: "#6ee7b7", variant: "success" },
  discovery: { bg: "#78350f", border: "#f59e0b", text: "#fcd34d", variant: "warning" },
  learning: { bg: "#4c1d95", border: "#8b5cf6", text: "#c4b5fd", variant: "purple" },
  observation: { bg: "#1e293b", border: "#475569", text: "#cbd5e1", variant: "secondary" },
  session: { bg: "#134e4a", border: "#14b8a6", text: "#5eead4", variant: "success" },
  code_entity: { bg: "#164e63", border: "#06b6d4", text: "#a5f3fc", variant: "default" },
  entity: { bg: "#312e81", border: "#6366f1", text: "#c7d2fe", variant: "purple" },
};

export default function GraphPage() {
  const { client } = useAuth();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const minimapRef = useRef<HTMLCanvasElement | null>(null);

  const [observations, setObservations] = useState<Observation[]>([]);
  const [selectedObsId, setSelectedObsId] = useState<string>("");
  const [depth, setDepth] = useState<number>(2);
  const [maxNodes, setMaxNodes] = useState<number>(80);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // View Mode: subgraph (2D physics), communities (Louvain), blast (blast radius), analytics (diagnostics)
  const [viewMode, setViewMode] = useState<"subgraph" | "communities" | "blast" | "analytics">("subgraph");

  // Graph Analytics & Blast Radius state
  const [analyticsReport, setAnalyticsReport] = useState<GraphAnalyticsReport | null>(null);
  const [analyticsLoading, setAnalyticsLoading] = useState(false);
  const [blastData, setBlastData] = useState<BlastRadiusResult | null>(null);
  const [blastLoading, setBlastLoading] = useState(false);
  const [activeCommunityId, setActiveCommunityId] = useState<number | null>(null);

  // Graph Simulation State
  const nodesRef = useRef<SimulationNode[]>([]);
  const edgesRef = useRef<SimulationLink[]>([]);
  const rootIdRef = useRef<string>("");
  const [selectedNode, setSelectedNode] = useState<SimulationNode | null>(null);
  const [hoveredNode, setHoveredNode] = useState<SimulationNode | null>(null);
  const [searchQuery, setSearchQuery] = useState("");
  const [activeFilters, setActiveFilters] = useState<Record<string, boolean>>({
    references: true,
    supersedes: true,
    contradicts: true,
    follows: true,
    relates_to: true,
    caused_by: true,
    calls: true,
    imports: true,
    implements: true,
    defines: true,
    uses: true,
  });

  // Canvas View Transform
  const [zoom, setZoom] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const isDraggingRef = useRef(false);
  const dragStartRef = useRef({ x: 0, y: 0 });
  const draggedNodeRef = useRef<SimulationNode | null>(null);
  const animationFrameId = useRef<number | null>(null);
  const alphaRef = useRef(1);

  // Copied indicator
  const [copied, setCopied] = useState(false);

  // Modals
  const [isResolveModalOpen, setIsResolveModalOpen] = useState(false);
  const [obsoleteObsId, setObsoleteObsId] = useState("");
  const [resolveReason, setResolveReason] = useState("");
  const [isResolving, setIsResolving] = useState(false);

  const [isConnectModalOpen, setIsConnectModalOpen] = useState(false);
  const [targetObsId, setTargetObsId] = useState("");
  const [relationType, setRelationType] = useState("relates_to");
  const [relationReason, setRelationReason] = useState("");
  const [isConnecting, setIsConnecting] = useState(false);

  // Initial Load of observations for the selector
  useEffect(() => {
    if (!client) return;
    client
      .listObservations("?limit=100")
      .then((obs) => {
        const list = obs || [];
        setObservations(list);
        if (list.length > 0 && !selectedObsId) {
          setSelectedObsId(list[0].id);
        }
      })
      .catch((err) => console.error("Failed to list observations", err));
  }, [client]);

  // Load Subgraph data from backend
  const loadSubgraph = useCallback(
    async (rootId: string, currentDepth = depth, currentMax = maxNodes) => {
      if (!client || !rootId) return;
      setLoading(true);
      setError(null);
      try {
        const data: GraphSubgraph = await client.subgraph(rootId, currentDepth, currentMax);
        rootIdRef.current = data.root;

        const existingPosMap = new Map<string, { x: number; y: number; vx: number; vy: number; isPinned?: boolean }>();
        nodesRef.current.forEach((n) => {
          existingPosMap.set(n.id, { x: n.x, y: n.y, vx: n.vx, vy: n.vy, isPinned: n.isPinned });
        });

        const centerX = 400;
        const centerY = 300;
        const count = data.nodes.length;

        const simNodes: SimulationNode[] = data.nodes.map((n, idx) => {
          const prev = existingPosMap.get(n.id);
          const isRoot = n.id === data.root;
          const radius = isRoot ? 26 : n.hop === 1 ? 20 : 16;

          if (prev) {
            return {
              ...n,
              x: prev.x,
              y: prev.y,
              vx: prev.vx || 0,
              vy: prev.vy || 0,
              radius,
              isPinned: prev.isPinned,
            };
          }

          const angle = (idx / (count || 1)) * 2 * Math.PI;
          const dist = isRoot ? 0 : n.hop === 1 ? 140 + Math.random() * 40 : 250 + Math.random() * 60;
          return {
            ...n,
            x: centerX + dist * Math.cos(angle) + (Math.random() - 0.5) * 20,
            y: centerY + dist * Math.sin(angle) + (Math.random() - 0.5) * 20,
            vx: 0,
            vy: 0,
            radius,
          };
        });

        const nodeMap = new Map<string, SimulationNode>();
        simNodes.forEach((n) => nodeMap.set(n.id, n));

        const simEdges: SimulationLink[] = data.edges
          .map((e) => ({
            ...e,
            sourceNode: nodeMap.get(e.source),
            targetNode: nodeMap.get(e.target),
          }))
          .filter((e) => e.sourceNode && e.targetNode);

        nodesRef.current = simNodes;
        edgesRef.current = simEdges;
        alphaRef.current = 1.0;

        const rootNode = simNodes.find((n) => n.id === data.root) || null;
        setSelectedNode(rootNode);
      } catch (err: any) {
        setError(err.message || "Error al cargar el subgrafo");
      } finally {
        setLoading(false);
      }
    },
    [client, depth, maxNodes],
  );

  useEffect(() => {
    if (selectedObsId) {
      loadSubgraph(selectedObsId);
    }
  }, [selectedObsId, loadSubgraph]);

  // Load Graph Analytics
  const loadAnalytics = useCallback(async () => {
    if (!client) return;
    setAnalyticsLoading(true);
    try {
      const report = await client.analytics();
      setAnalyticsReport(report);
    } catch (err: any) {
      console.error("Failed to load graph analytics", err);
    } finally {
      setAnalyticsLoading(false);
    }
  }, [client]);

  useEffect(() => {
    if (viewMode === "communities" || viewMode === "analytics") {
      loadAnalytics();
    }
  }, [viewMode, loadAnalytics]);

  // Calculate Blast Radius for Selected Node
  const handleInspectBlastRadius = async (nodeId: string) => {
    if (!client || !nodeId) return;
    setBlastLoading(true);
    setViewMode("blast");
    try {
      const res = await client.blastRadius(nodeId, 3);
      setBlastData(res);
    } catch (err: any) {
      alert("Error al calcular blast radius: " + (err.message || err));
    } finally {
      setBlastLoading(false);
    }
  };

  // Node to Community mapping
  const nodeCommunityMap = useMemo(() => {
    const map = new Map<string, number>();
    if (analyticsReport?.communities) {
      analyticsReport.communities.forEach((comm) => {
        comm.members.forEach((m) => map.set(m, comm.id));
      });
    }
    return map;
  }, [analyticsReport]);

  // Export Graph to Obsidian Markdown format
  const handleExportObsidian = () => {
    const nodes = nodesRef.current;
    const edges = edgesRef.current;
    if (nodes.length === 0) {
      alert("No hay nodos en el grafo para exportar.");
      return;
    }

    let md = `# Cortex Knowledge Graph Export\n\n`;
    md += `*Generado automáticamente: ${new Date().toLocaleString()}*\n\n`;
    md += `## Resumen del Grafo\n- Nodos totales: ${nodes.length}\n- Aristas totales: ${edges.length}\n\n`;

    md += `## Entidades y WikiLinks\n`;
    nodes.forEach((n) => {
      md += `### [[${n.label}]]\n`;
      md += `- **ID:** \`${n.id}\`\n`;
      md += `- **Tipo:** \`${n.kind}\`\n`;
      if (n.project) md += `- **Proyecto:** ${n.project}\n`;

      const outEdges = edges.filter((e) => e.source === n.id);
      if (outEdges.length > 0) {
        md += `- **Relaciones Salientes:**\n`;
        outEdges.forEach((e) => {
          const target = nodes.find((tn) => tn.id === e.target);
          if (target) {
            md += `  - ${e.type} ➔ [[${target.label}]]\n`;
          }
        });
      }
      md += `\n`;
    });

    const blob = new Blob([md], { type: "text/markdown;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `cortex_graph_obsidian_${new Date().toISOString().slice(0, 10)}.md`;
    a.click();
    URL.revokeObjectURL(url);
  };

  // 60 FPS Force-Directed Physics Engine
  useEffect(() => {
    let animationId: number;

    const stepSimulation = () => {
      const nodes = nodesRef.current;
      const edges = edgesRef.current;
      const alpha = alphaRef.current;

      if (alpha > 0.005) {
        // Repulsion (Coulomb)
        for (let i = 0; i < nodes.length; i++) {
          for (let j = i + 1; j < nodes.length; j++) {
            const n1 = nodes[i];
            const n2 = nodes[j];
            const dx = n2.x - n1.x;
            const dy = n2.y - n1.y;
            const distSq = dx * dx + dy * dy || 1;
            const dist = Math.sqrt(distSq);

            if (dist < 400) {
              const force = (1800 / distSq) * alpha;
              const fx = (dx / dist) * force;
              const fy = (dy / dist) * force;

              if (!n1.isPinned) {
                n1.vx -= fx;
                n1.vy -= fy;
              }
              if (!n2.isPinned) {
                n2.vx += fx;
                n2.vy += fy;
              }
            }
          }
        }

        // Attraction (Hooke)
        for (let i = 0; i < edges.length; i++) {
          const e = edges[i];
          if (!e.sourceNode || !e.targetNode) continue;
          const s = e.sourceNode;
          const t = e.targetNode;
          const dx = t.x - s.x;
          const dy = t.y - s.y;
          const dist = Math.sqrt(dx * dx + dy * dy) || 1;
          const idealDist = 120;
          const force = (dist - idealDist) * 0.045 * alpha;
          const fx = (dx / dist) * force;
          const fy = (dy / dist) * force;

          if (!s.isPinned) {
            s.vx += fx;
            s.vy += fy;
          }
          if (!t.isPinned) {
            t.vx -= fx;
            t.vy -= fy;
          }
        }

        // Center gravity & Damping
        const cx = 400;
        const cy = 300;
        for (let i = 0; i < nodes.length; i++) {
          const n = nodes[i];
          if (n.isPinned) continue;

          n.vx += (cx - n.x) * 0.003 * alpha;
          n.vy += (cy - n.y) * 0.003 * alpha;

          n.vx *= 0.88;
          n.vy *= 0.88;

          n.x += n.vx;
          n.y += n.vy;
        }

        alphaRef.current = alpha * 0.985;
      }

      draw();
      animationId = requestAnimationFrame(stepSimulation);
    };

    animationId = requestAnimationFrame(stepSimulation);
    return () => cancelAnimationFrame(animationId);
  }, []);

  // Main Canvas Drawing Loop
  const draw = () => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const width = canvas.width;
    const height = canvas.height;
    ctx.clearRect(0, 0, width, height);

    ctx.save();
    ctx.translate(offset.x, offset.y);
    ctx.scale(zoom, zoom);

    const nodes = nodesRef.current;
    const edges = edgesRef.current;
    const rootId = rootIdRef.current;
    const activeNode = hoveredNode || selectedNode;

    const connectedNodeIds = new Set<string>();
    if (activeNode) {
      connectedNodeIds.add(activeNode.id);
      edges.forEach((e) => {
        if (e.source === activeNode.id) connectedNodeIds.add(e.target);
        if (e.target === activeNode.id) connectedNodeIds.add(e.source);
      });
    }

    // Blast Radius sets
    const directImpactSet = new Set(blastData?.direct_impact || []);
    const totalImpactSet = new Set(blastData?.total_impacted || []);
    const isBlastMode = viewMode === "blast" && !!blastData;

    // Draw Edges
    edges.forEach((edge) => {
      if (!edge.sourceNode || !edge.targetNode) return;
      if (activeFilters[edge.type] === false) return;

      const isConnected = activeNode
        ? edge.source === activeNode.id || edge.target === activeNode.id
        : false;
      const isDimmed = activeNode ? !isConnected : false;

      ctx.save();
      ctx.beginPath();
      ctx.moveTo(edge.sourceNode.x, edge.sourceNode.y);
      ctx.lineTo(edge.targetNode.x, edge.targetNode.y);

      const color = RELATION_COLORS[edge.type] || DEFAULT_RELATION_COLOR;
      ctx.strokeStyle = color;
      ctx.lineWidth = isConnected ? 2.5 : 1.2;
      ctx.globalAlpha = isDimmed ? 0.15 : isConnected ? 0.95 : 0.45;

      if (edge.type === "contradicts") {
        ctx.setLineDash([4, 4]);
      }
      ctx.stroke();

      // Direction Arrow
      const dx = edge.targetNode.x - edge.sourceNode.x;
      const dy = edge.targetNode.y - edge.sourceNode.y;
      const angle = Math.atan2(dy, dx);
      const targetRadius = edge.targetNode.radius;
      const arrowX = edge.targetNode.x - Math.cos(angle) * (targetRadius + 4);
      const arrowY = edge.targetNode.y - Math.sin(angle) * (targetRadius + 4);

      ctx.beginPath();
      ctx.moveTo(arrowX, arrowY);
      ctx.lineTo(
        arrowX - 8 * Math.cos(angle - Math.PI / 6),
        arrowY - 8 * Math.sin(angle - Math.PI / 6),
      );
      ctx.lineTo(
        arrowX - 8 * Math.cos(angle + Math.PI / 6),
        arrowY - 8 * Math.sin(angle + Math.PI / 6),
      );
      ctx.closePath();
      ctx.fillStyle = color;
      ctx.fill();

      ctx.restore();
    });

    // Draw Nodes
    nodes.forEach((node) => {
      const isRoot = node.id === rootId;
      const isSelected = selectedNode?.id === node.id;
      const isSearchMatch = searchQuery && node.label.toLowerCase().includes(searchQuery.toLowerCase());
      const isConnected = activeNode ? connectedNodeIds.has(node.id) : true;

      let isDimmed = activeNode ? !connectedNodeIds.has(node.id) : false;

      // Blast Radius logic
      let isBlastRoot = false;
      let isDirectImpact = false;
      let isTotalImpact = false;

      if (isBlastMode) {
        if (node.id === blastData?.root_node) {
          isBlastRoot = true;
        } else if (directImpactSet.has(node.id)) {
          isDirectImpact = true;
        } else if (totalImpactSet.has(node.id)) {
          isTotalImpact = true;
        } else {
          isDimmed = true;
        }
      }

      // Community Mode logic
      const commId = nodeCommunityMap.get(node.id);
      const isCommunityMode = viewMode === "communities" && commId !== undefined;
      const commColor = commId !== undefined ? COMMUNITY_COLORS[commId % COMMUNITY_COLORS.length] : null;

      const kindStyle = KIND_COLORS[node.kind] || KIND_COLORS.observation;

      ctx.save();
      if (isDimmed && !isSearchMatch && !isBlastRoot && !isDirectImpact && !isTotalImpact) {
        ctx.globalAlpha = 0.2;
      }

      // Outer Glow
      if (isRoot || isSelected || isSearchMatch || isBlastRoot || isDirectImpact) {
        ctx.beginPath();
        ctx.arc(node.x, node.y, node.radius + (isSelected ? 8 : 5), 0, 2 * Math.PI);
        ctx.fillStyle = isBlastRoot
          ? "rgba(239, 68, 68, 0.4)"
          : isDirectImpact
          ? "rgba(245, 158, 11, 0.35)"
          : isRoot
          ? "rgba(59, 130, 246, 0.25)"
          : isSearchMatch
          ? "rgba(245, 158, 11, 0.25)"
          : "rgba(96, 165, 250, 0.25)";
        ctx.fill();
      }

      // Node Body
      ctx.beginPath();
      ctx.arc(node.x, node.y, node.radius, 0, 2 * Math.PI);

      if (isBlastRoot) {
        ctx.fillStyle = "#ef4444";
      } else if (isDirectImpact) {
        ctx.fillStyle = "#f59e0b";
      } else if (isTotalImpact) {
        ctx.fillStyle = "#fb923c";
      } else if (isCommunityMode && commColor) {
        ctx.fillStyle = commColor;
      } else if (isRoot) {
        ctx.fillStyle = "#2563eb";
      } else if (isSelected) {
        ctx.fillStyle = "#3b82f6";
      } else {
        ctx.fillStyle = kindStyle.bg;
      }
      ctx.fill();

      // Node Border
      ctx.lineWidth = isSelected ? 3.5 : isRoot || isBlastRoot ? 2.5 : 1.5;
      ctx.strokeStyle = isBlastRoot
        ? "#fca5a5"
        : isDirectImpact
        ? "#fde68a"
        : isSelected
        ? "#93c5fd"
        : isRoot
        ? "#60a5fa"
        : isCommunityMode && commColor
        ? "#ffffff"
        : kindStyle.border;
      ctx.stroke();

      // Pin indicator
      if (node.isPinned) {
        ctx.fillStyle = "#f59e0b";
        ctx.beginPath();
        ctx.arc(node.x + node.radius * 0.7, node.y - node.radius * 0.7, 3.5, 0, 2 * Math.PI);
        ctx.fill();
      }

      // Hop Ring
      if (!isRoot && node.hop > 1) {
        ctx.strokeStyle = "rgba(148, 163, 184, 0.4)";
        ctx.lineWidth = 1;
        ctx.setLineDash([2, 2]);
        ctx.beginPath();
        ctx.arc(node.x, node.y, node.radius + 3, 0, 2 * Math.PI);
        ctx.stroke();
        ctx.setLineDash([]);
      }

      // Label
      ctx.fillStyle = isSelected ? "#ffffff" : isDimmed ? "#64748b" : "#f8fafc";
      ctx.font = isRoot || isBlastRoot
        ? "bold 12px Inter, sans-serif"
        : isSelected
        ? "600 11px Inter, sans-serif"
        : "11px Inter, sans-serif";
      ctx.textAlign = "center";
      ctx.textBaseline = "top";

      const maxLen = isRoot ? 24 : 18;
      const labelText = node.label.length > maxLen ? node.label.slice(0, maxLen - 2) + "..." : node.label;
      ctx.fillText(labelText, node.x, node.y + node.radius + 6);

      if (zoom >= 0.85) {
        ctx.fillStyle = isCommunityMode ? "#ffffff" : kindStyle.text;
        ctx.font = "9px Inter, sans-serif";
        ctx.fillText(`[${node.kind}]`, node.x, node.y + node.radius + 20);
      }

      ctx.restore();
    });

    ctx.restore();

    // Radar Minimap
    drawRadarHUD();
  };

  const drawRadarHUD = () => {
    const minimap = minimapRef.current;
    if (!minimap) return;
    const mctx = minimap.getContext("2d");
    if (!mctx) return;

    mctx.clearRect(0, 0, minimap.width, minimap.height);
    const nodes = nodesRef.current;
    if (nodes.length === 0) return;

    let minX = Infinity,
      maxX = -Infinity,
      minY = Infinity,
      maxY = -Infinity;
    nodes.forEach((n) => {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    });

    const pad = 80;
    minX -= pad;
    maxX += pad;
    minY -= pad;
    maxY += pad;

    const gw = maxX - minX || 1;
    const gh = maxY - minY || 1;
    const sx = minimap.width / gw;
    const sy = minimap.height / gh;
    const scale = Math.min(sx, sy);

    nodes.forEach((n) => {
      const mx = (n.x - minX) * scale;
      const my = (n.y - minY) * scale;
      mctx.beginPath();
      mctx.arc(mx, my, 2.5, 0, 2 * Math.PI);
      mctx.fillStyle = n.id === rootIdRef.current ? "#3b82f6" : "#64748b";
      mctx.fill();
    });
  };

  // Canvas Interactions
  const handleMouseDown = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;

    const clicked = nodesRef.current.find((n) => {
      const dx = n.x - mouseX;
      const dy = n.y - mouseY;
      return dx * dx + dy * dy <= n.radius * n.radius;
    });

    if (clicked) {
      draggedNodeRef.current = clicked;
      clicked.isPinned = true;
    } else {
      isDraggingRef.current = true;
      dragStartRef.current = { x: e.clientX - offset.x, y: e.clientY - offset.y };
    }
  };

  const handleMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;

    if (draggedNodeRef.current) {
      draggedNodeRef.current.x = mouseX;
      draggedNodeRef.current.y = mouseY;
      alphaRef.current = Math.max(alphaRef.current, 0.4);
    } else if (isDraggingRef.current) {
      setOffset({
        x: e.clientX - dragStartRef.current.x,
        y: e.clientY - dragStartRef.current.y,
      });
    } else {
      const hovered = nodesRef.current.find((n) => {
        const dx = n.x - mouseX;
        const dy = n.y - mouseY;
        return dx * dx + dy * dy <= n.radius * n.radius;
      });
      setHoveredNode(hovered || null);
    }
  };

  const handleMouseUp = () => {
    draggedNodeRef.current = null;
    isDraggingRef.current = false;
  };

  const handleCanvasClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;

    const clicked = nodesRef.current.find((n) => {
      const dx = n.x - mouseX;
      const dy = n.y - mouseY;
      return dx * dx + dy * dy <= n.radius * n.radius;
    });

    setSelectedNode(clicked || null);
  };

  const handleDoubleClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const rect = canvasRef.current?.getBoundingClientRect();
    if (!rect) return;
    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;

    const clicked = nodesRef.current.find((n) => {
      const dx = n.x - mouseX;
      const dy = n.y - mouseY;
      return dx * dx + dy * dy <= n.radius * n.radius;
    });

    if (clicked) {
      clicked.isPinned = !clicked.isPinned;
    }
  };

  const handleWheel = (e: React.WheelEvent<HTMLCanvasElement>) => {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.12 : 0.88;
    setZoom((z) => Math.min(Math.max(z * factor, 0.25), 3.5));
  };

  const handleFitView = () => {
    const canvas = canvasRef.current;
    const nodes = nodesRef.current;
    if (!canvas || nodes.length === 0) return;

    let minX = Infinity,
      maxX = -Infinity,
      minY = Infinity,
      maxY = -Infinity;
    nodes.forEach((n) => {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    });

    const padding = 60;
    const graphWidth = maxX - minX + padding * 2;
    const graphHeight = maxY - minY + padding * 2;

    const scaleX = canvas.width / graphWidth;
    const scaleY = canvas.height / graphHeight;
    const newZoom = Math.min(scaleX, scaleY, 1.5);

    const centerX = (minX + maxX) / 2;
    const centerY = (minY + maxY) / 2;

    setZoom(newZoom);
    setOffset({
      x: canvas.width / 2 - centerX * newZoom,
      y: canvas.height / 2 - centerY * newZoom,
    });
  };

  const handleReheatSimulation = () => {
    alphaRef.current = 1.0;
  };

  const focusOnNode = (node: SimulationNode) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    setZoom(1.2);
    setOffset({
      x: canvas.width / 2 - node.x * 1.2,
      y: canvas.height / 2 - node.y * 1.2,
    });
    setSelectedNode(node);
  };

  const handleExpandNode = async () => {
    if (!client || !selectedNode) return;
    setLoading(true);
    try {
      const data: GraphSubgraph = await client.subgraph(selectedNode.id, 1, 20);
      const existingNodeIds = new Set(nodesRef.current.map((n) => n.id));
      const existingEdgeKeys = new Set(nodesRef.current.map((e) => `${e.id}`));

      const newNodes: SimulationNode[] = [];
      data.nodes.forEach((n) => {
        if (!existingNodeIds.has(n.id)) {
          const angle = Math.random() * 2 * Math.PI;
          const dist = 100 + Math.random() * 40;
          newNodes.push({
            ...n,
            x: selectedNode.x + dist * Math.cos(angle),
            y: selectedNode.y + dist * Math.sin(angle),
            vx: 0,
            vy: 0,
            radius: 16,
          });
        }
      });

      const nodeMap = new Map<string, SimulationNode>();
      [...nodesRef.current, ...newNodes].forEach((n) => nodeMap.set(n.id, n));

      const newEdges: SimulationLink[] = [];
      data.edges.forEach((e) => {
        const key = `${e.source}->${e.target}`;
        if (!existingEdgeKeys.has(key)) {
          const src = nodeMap.get(e.source);
          const tgt = nodeMap.get(e.target);
          if (src && tgt) {
            newEdges.push({ ...e, sourceNode: src, targetNode: tgt });
          }
        }
      });

      nodesRef.current = [...nodesRef.current, ...newNodes];
      edgesRef.current = [...edgesRef.current, ...newEdges];
      alphaRef.current = 0.8;
    } catch (err: any) {
      alert("Error al expandir subgrafo: " + (err.message || err));
    } finally {
      setLoading(false);
    }
  };

  const selectedNodeNeighbors = useMemo(() => {
    if (!selectedNode) return [];
    const neighbors: { node: SimulationNode; relation: string; direction: "in" | "out" }[] = [];
    const nodeMap = new Map(nodesRef.current.map((n) => [n.id, n]));

    edgesRef.current.forEach((e) => {
      if (e.source === selectedNode.id) {
        const target = nodeMap.get(e.target);
        if (target) neighbors.push({ node: target, relation: e.type, direction: "out" });
      } else if (e.target === selectedNode.id) {
        const source = nodeMap.get(e.source);
        if (source) neighbors.push({ node: source, relation: e.type, direction: "in" });
      }
    });
    return neighbors;
  }, [selectedNode]);

  const handleResolveConflict = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !selectedNode || !obsoleteObsId) return;
    setIsResolving(true);
    try {
      await client.resolveConflict({
        new_observation_id: selectedNode.id,
        obsolete_observation_id: obsoleteObsId,
        reason: resolveReason || "Superado por nuevo conocimiento",
      });
      setIsResolveModalOpen(false);
      setResolveReason("");
      loadSubgraph(selectedNode.id);
      alert("¡Conflicto resuelto exitosamente! Arista 'supersedes' registrada.");
    } catch (err: any) {
      alert("Error al resolver: " + (err.message || err));
    } finally {
      setIsResolving(false);
    }
  };

  const handleCreateEdge = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !selectedNode || !targetObsId) return;
    setIsConnecting(true);
    try {
      await client.createEdge({
        from_id: selectedNode.id,
        to_id: targetObsId,
        relation_type: relationType,
        reasoning: relationReason || "Conexión manual establecida desde el explorador de grafos",
      });
      setIsConnectModalOpen(false);
      setRelationReason("");
      loadSubgraph(selectedNode.id);
      alert("¡Arista creada exitosamente!");
    } catch (err: any) {
      alert("Error al conectar: " + (err.message || err));
    } finally {
      setIsConnecting(false);
    }
  };

  const copyNodeId = (id: string) => {
    navigator.clipboard.writeText(id);
    setCopied(true);
    setTimeout(() => setCopied(false), 1800);
  };

  const stats = useMemo(() => {
    const nodes = nodesRef.current;
    const edges = edgesRef.current;
    const contradictsCount = edges.filter((e) => e.type === "contradicts").length;
    const supersedesCount = edges.filter((e) => e.type === "supersedes").length;
    return {
      nodes: nodes.length,
      edges: edges.length,
      contradicts: contradictsCount,
      supersedes: supersedesCount,
    };
  }, [nodesRef.current.length, edgesRef.current.length]);

  return (
    <div className="space-y-5">
      {/* Page Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text-primary)] flex items-center gap-2.5">
            <Share2 className="h-5 w-5 sm:h-6 sm:w-6 text-blue-500 shrink-0" />
            <span>Grafo de Conocimiento & Código</span>
          </h1>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Motor de física 2D a 60 FPS con clustering modular (Louvain), análisis de blast radius y detección de olores arquitectónicos.
          </p>
        </div>

        {/* Global Toolbar */}
        <div className="flex flex-wrap items-center gap-2.5">
          <div className="flex items-center gap-1.5 sm:gap-2">
            <span className="text-[11px] font-semibold text-[var(--text-muted)]">RAÍZ:</span>
            <Select
              value={selectedObsId}
              onChange={(e) => setSelectedObsId(e.target.value)}
              className="w-48 sm:w-56 text-xs"
            >
              {observations.map((o) => (
                <option key={o.id} value={o.id}>
                  {o.title} ({o.project})
                </option>
              ))}
            </Select>
          </div>

          <div className="flex items-center gap-1.5 sm:gap-2">
            <span className="text-[11px] font-semibold text-[var(--text-muted)]">SALTOS:</span>
            <Select
              value={depth}
              onChange={(e) => setDepth(Number(e.target.value))}
              className="w-20 sm:w-24 text-xs"
            >
              <option value={1}>1 hop</option>
              <option value={2}>2 hops</option>
              <option value={3}>3 hops</option>
              <option value={4}>4 hops</option>
            </Select>
          </div>

          <Button
            onClick={() => loadSubgraph(selectedObsId)}
            variant="secondary"
            size="sm"
            disabled={loading}
            title="Recargar Grafo"
            className="text-xs gap-1.5"
          >
            <RotateCcw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
            <span>{loading ? "Cargando..." : "Recargar"}</span>
          </Button>

          <Button
            onClick={handleExportObsidian}
            variant="outline"
            size="sm"
            className="text-xs gap-1.5 border-[var(--border-subtle)] bg-[var(--bg-surface)]"
            title="Exportar a Obsidian Vault en Markdown con [[WikiLinks]]"
          >
            <Download className="h-3.5 w-3.5 text-purple-400" />
            <span>Obsidian (.md)</span>
          </Button>
        </div>
      </div>

      {/* Mode Switcher Tabs */}
      <div className="flex flex-wrap items-center gap-2 border-b border-[var(--border-subtle)] pb-2">
        <button
          type="button"
          onClick={() => {
            setViewMode("subgraph");
            setBlastData(null);
          }}
          className={`px-3 py-1.5 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
            viewMode === "subgraph"
              ? "bg-[var(--accent-primary)] text-white shadow-md shadow-blue-600/20"
              : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
          }`}
        >
          <Network className="h-3.5 w-3.5" />
          <span>Subgrafo 2D</span>
        </button>

        <button
          type="button"
          onClick={() => {
            setViewMode("communities");
            setBlastData(null);
          }}
          className={`px-3 py-1.5 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
            viewMode === "communities"
              ? "bg-purple-600 text-white shadow-md shadow-purple-600/20"
              : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
          }`}
        >
          <Layers className="h-3.5 w-3.5 text-purple-300" />
          <span>Comunidades Louvain ({analyticsReport?.communities?.length || 0})</span>
        </button>

        <button
          type="button"
          onClick={() => {
            if (selectedNode) {
              handleInspectBlastRadius(selectedNode.id);
            } else {
              setViewMode("blast");
            }
          }}
          className={`px-3 py-1.5 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
            viewMode === "blast"
              ? "bg-rose-600 text-white shadow-md shadow-rose-600/20"
              : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
          }`}
        >
          <Zap className="h-3.5 w-3.5 text-amber-300" />
          <span>Blast Radius (Impacto)</span>
        </button>

        <button
          type="button"
          onClick={() => {
            setViewMode("analytics");
            setBlastData(null);
          }}
          className={`px-3 py-1.5 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
            viewMode === "analytics"
              ? "bg-emerald-600 text-white shadow-md shadow-emerald-600/20"
              : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
          }`}
        >
          <Activity className="h-3.5 w-3.5 text-emerald-300" />
          <span>Diagnóstico Arquitectónico</span>
        </button>
      </div>

      {/* Metrics Bar */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3.5 sm:gap-4">
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-md">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Nodos en el Grafo</span>
          <span className="text-2xl font-bold text-[var(--text-primary)] mt-1 block">{stats.nodes}</span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-md">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Aristas de Relación</span>
          <span className="text-2xl font-bold text-blue-400 mt-1 block">{stats.edges}</span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-md">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Conflictos Detectados</span>
          <span className={`text-2xl font-bold mt-1 block ${stats.contradicts > 0 ? "text-red-400" : "text-[var(--text-muted)]"}`}>
            {stats.contradicts}
          </span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-md">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Comunidades Funcionales</span>
          <span className="text-2xl font-bold text-purple-400 mt-1 block">
            {analyticsReport?.communities?.length || 1}
          </span>
        </Card>
      </div>

      {/* Relation Type Filter Chips & Search Bar */}
      <Card className="p-3 bg-[var(--bg-secondary)] border-[var(--border-subtle)] flex flex-wrap items-center justify-between gap-3 shadow-md">
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex items-center gap-1.5 text-xs font-semibold text-[var(--text-muted)] mr-1">
            <Filter className="h-3.5 w-3.5" />
            <span>FILTROS:</span>
          </div>
          {Object.entries(RELATION_COLORS).map(([rel, color]) => {
            const active = activeFilters[rel] !== false;
            return (
              <button
                key={rel}
                type="button"
                onClick={() => setActiveFilters((prev) => ({ ...prev, [rel]: !active }))}
                className="inline-flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full border transition-all duration-150"
                style={{
                  borderColor: active ? color : "var(--border-subtle)",
                  backgroundColor: active ? `${color}20` : "transparent",
                  color: active ? color : "var(--text-secondary)",
                  fontWeight: active ? "600" : "400",
                }}
              >
                <span
                  className="w-2 h-2 rounded-full"
                  style={{ backgroundColor: active ? color : "#64748b" }}
                />
                <span>{rel}</span>
              </button>
            );
          })}
        </div>

        <div className="relative w-full sm:w-64">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-[var(--text-muted)]" />
          <Input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Buscar nodo en grafo..."
            className="pl-8 h-8 text-xs w-full"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery("")}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-[var(--text-primary)]"
            >
              <X className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
      </Card>

      {/* Main Workspace Layout */}
      <div className="grid grid-cols-1 lg:grid-cols-[1fr_380px] gap-4 sm:gap-5">
        {/* Canvas Explorer Card */}
        <Card
          ref={containerRef}
          className="relative p-0 overflow-hidden min-h-[420px] sm:min-h-[500px] h-[55vh] lg:h-[calc(100vh-340px)] flex flex-col border-[var(--border-subtle)] bg-[var(--bg-primary)] shadow-2xl"
        >
          {/* Floating Canvas Action Overlay */}
          <div className="absolute top-3 sm:top-4 left-3 sm:left-4 flex flex-wrap items-center gap-1.5 sm:gap-2 z-10">
            <Button onClick={() => setZoom((z) => Math.min(z * 1.25, 3.5))} variant="secondary" size="icon" className="h-8 w-8" title="Acercar">
              <ZoomIn className="h-4 w-4" />
            </Button>
            <Button onClick={() => setZoom((z) => Math.max(z * 0.8, 0.25))} variant="secondary" size="icon" className="h-8 w-8" title="Alejar">
              <ZoomOut className="h-4 w-4" />
            </Button>
            <Button onClick={handleFitView} variant="secondary" size="icon" className="h-8 w-8" title="Ajustar Vista al Grafo">
              <Maximize2 className="h-4 w-4" />
            </Button>
            <Button onClick={handleReheatSimulation} variant="secondary" size="icon" className="h-8 w-8" title="Reorganizar Físicas">
              <Flame className="h-4 w-4 text-amber-500" />
            </Button>
            {selectedNode && (
              <Button onClick={() => focusOnNode(selectedNode)} variant="secondary" size="sm" className="h-8 text-xs" title="Centrar en Nodo Seleccionado">
                <Compass className="h-3.5 w-3.5 text-blue-400 mr-1" />
                <span>Centrar</span>
              </Button>
            )}
          </div>

          {/* Blast Radius HUD overlay */}
          {viewMode === "blast" && blastData && (
            <div className="absolute top-3 sm:top-4 right-3 sm:right-4 z-10 p-3 rounded-xl bg-rose-950/80 border border-rose-800/60 backdrop-blur-md text-xs space-y-1 shadow-2xl max-w-xs">
              <div className="flex items-center gap-1.5 text-rose-400 font-bold">
                <Zap className="h-4 w-4" />
                <span>Blast Radius: {blastData.blast_radius_pct.toFixed(1)}% del Grafo</span>
              </div>
              <div className="text-[11px] text-slate-300">
                • Impacto Directo: <b>{blastData.direct_impact.length}</b> nodos
              </div>
              <div className="text-[11px] text-slate-300">
                • Impacto Total: <b>{blastData.total_impacted.length}</b> nodos
              </div>
              <div className="text-[11px] text-slate-300">
                • Archivos Afectados: <b>{blastData.impacted_files.length}</b>
              </div>
            </div>
          )}

          {/* Mini-Map Radar HUD */}
          <div className="absolute bottom-3 sm:bottom-4 right-3 sm:right-4 z-10 bg-[var(--bg-secondary)]/90 backdrop-blur-md p-2 rounded-xl border border-[var(--border-subtle)] shadow-2xl hidden sm:block">
            <div className="text-[10px] font-semibold text-[var(--text-muted)] mb-1 uppercase tracking-wider">
              Radar HUD
            </div>
            <canvas ref={minimapRef} width={140} height={100} className="block rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]" />
          </div>

          {/* Interactive Canvas */}
          <canvas
            ref={canvasRef}
            onMouseDown={handleMouseDown}
            onMouseMove={handleMouseMove}
            onMouseUp={handleMouseUp}
            onClick={handleCanvasClick}
            onDoubleClick={handleDoubleClick}
            onWheel={handleWheel}
            className="w-full h-full block cursor-grab active:cursor-grabbing"
          />
        </Card>

        {/* Right Panel: Node Inspector or Architectural Diagnostics */}
        <Card className="flex flex-col justify-between min-h-[300px] lg:h-[calc(100vh-340px)] overflow-y-auto border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 sm:p-5 shadow-2xl">
          {viewMode === "analytics" ? (
            /* Architectural Intelligence Panel */
            <div className="space-y-4 text-xs">
              <div className="pb-3 border-b border-[var(--border-subtle)] flex items-center justify-between">
                <h2 className="text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
                  <Activity className="h-4 w-4 text-emerald-400" />
                  Diagnóstico Arquitectónico
                </h2>
                <Button variant="ghost" size="sm" onClick={loadAnalytics} disabled={analyticsLoading} className="text-xs">
                  <RotateCcw className={`h-3 w-3 ${analyticsLoading ? "animate-spin" : ""}`} />
                </Button>
              </div>

              {analyticsLoading ? (
                <div className="py-12 text-center text-[var(--text-muted)]">Calculando métricas...</div>
              ) : analyticsReport ? (
                <div className="space-y-4">
                  {/* God Nodes */}
                  <div className="space-y-1.5">
                    <span className="text-[10px] font-bold text-amber-400 uppercase tracking-wider block">
                      🔥 GOD NODES (CUELLOS DE BOTELLA)
                    </span>
                    <div className="space-y-1 max-h-36 overflow-y-auto pr-1">
                      {analyticsReport.god_nodes.map((gn) => (
                        <div
                          key={gn.id}
                          onClick={() => {
                            const n = nodesRef.current.find((node) => node.id === gn.id);
                            if (n) focusOnNode(n);
                          }}
                          className="p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] hover:border-amber-500/40 cursor-pointer flex items-center justify-between transition-all"
                        >
                          <span className="font-semibold text-slate-200 truncate">{gn.label}</span>
                          <Badge variant="warning" className="text-[10px]">Grado {gn.degree}</Badge>
                        </div>
                      ))}
                    </div>
                  </div>

                  {/* Surprising Connections */}
                  <div className="space-y-1.5">
                    <span className="text-[10px] font-bold text-purple-400 uppercase tracking-wider block">
                      ⚡ CONEXIONES SORPRENDENTES
                    </span>
                    <div className="space-y-1 max-h-36 overflow-y-auto pr-1">
                      {analyticsReport.surprising_connections.length === 0 ? (
                        <p className="text-[11px] text-[var(--text-muted)] italic">Sin anomalías estructurales</p>
                      ) : (
                        analyticsReport.surprising_connections.map((sc, idx) => (
                          <div key={idx} className="p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] space-y-1">
                            <div className="flex items-center justify-between text-[11px]">
                              <span className="font-mono text-purple-300 truncate">{sc.source_node} ➔ {sc.target_node}</span>
                              <Badge variant="purple" className="text-[9px]">Score {sc.score}</Badge>
                            </div>
                            <p className="text-[10px] text-[var(--text-muted)]">{sc.reasons.join(", ")}</p>
                          </div>
                        ))
                      )}
                    </div>
                  </div>

                  {/* Cycles */}
                  <div className="space-y-1.5">
                    <span className="text-[10px] font-bold text-rose-400 uppercase tracking-wider block">
                      🔄 CICLOS DE DEPENDENCIA
                    </span>
                    <div className="space-y-1 max-h-28 overflow-y-auto pr-1">
                      {analyticsReport.cycles.length === 0 ? (
                        <p className="text-[11px] text-emerald-400">✓ 0 dependencias circulares detectadas</p>
                      ) : (
                        analyticsReport.cycles.map((c, idx) => (
                          <div key={idx} className="p-2 rounded-lg bg-rose-950/40 border border-rose-900/50 text-rose-300 text-[10px] font-mono">
                            {c.nodes.join(" ➔ ")}
                          </div>
                        ))
                      )}
                    </div>
                  </div>
                </div>
              ) : null}
            </div>
          ) : (
            /* Node Inspector Drawer */
            <div className="space-y-4">
              <div className="flex items-center justify-between pb-3 border-b border-[var(--border-subtle)]">
                <h2 className="text-sm font-semibold text-[var(--text-primary)] flex items-center gap-2">
                  <Info className="h-4 w-4 text-blue-400" />
                  Detalle del Nodo
                </h2>
                {selectedNode && (
                  <Badge variant="default">Hop {selectedNode.hop}</Badge>
                )}
              </div>

              {selectedNode ? (
                <div className="space-y-3.5 text-xs">
                  <div>
                    <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-wider block">TÍTULO</span>
                    <div className="font-semibold text-sm text-slate-100 mt-1">
                      {selectedNode.label}
                    </div>
                  </div>

                  <div className="flex items-center justify-between">
                    <div>
                      <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-wider block">TIPO DE ENTIDAD</span>
                      <div className="mt-1">
                        <Badge variant={KIND_COLORS[selectedNode.kind]?.variant || "secondary"}>
                          {selectedNode.kind}
                        </Badge>
                      </div>
                    </div>

                    <div>
                      <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-wider block">ESTADO PIN</span>
                      <div className="mt-1 text-slate-400">
                        {selectedNode.isPinned ? "📌 Anclado" : "Dinámico"}
                      </div>
                    </div>
                  </div>

                  <div>
                    <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-wider block">ID OPACO</span>
                    <div
                      onClick={() => copyNodeId(selectedNode.id)}
                      className="flex items-center justify-between p-2 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] hover:border-slate-700 cursor-pointer mt-1 transition-colors"
                      title="Haz clic para copiar"
                    >
                      <span className="font-mono text-[11px] text-slate-300 overflow-hidden text-ellipsis whitespace-nowrap">
                        {selectedNode.id}
                      </span>
                      {copied ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5 text-slate-500" />}
                    </div>
                  </div>

                  <div>
                    <span className="text-[10px] font-semibold text-slate-400 uppercase tracking-wider block">
                      VECINOS CONECTADOS ({selectedNodeNeighbors.length})
                    </span>
                    <div className="mt-1.5 space-y-1.5 max-h-32 overflow-y-auto pr-1">
                      {selectedNodeNeighbors.length === 0 ? (
                        <p className="text-xs text-slate-500 italic">Sin conexiones directas visibles</p>
                      ) : (
                        selectedNodeNeighbors.map(({ node, relation, direction }, idx) => (
                          <div
                            key={idx}
                            onClick={() => focusOnNode(node)}
                            className="flex items-center justify-between p-2 bg-[var(--bg-surface)] rounded-lg border border-[var(--border-subtle)] hover:border-slate-600 cursor-pointer transition-all"
                          >
                            <div className="flex items-center gap-2 overflow-hidden">
                              <span className="text-[10px] font-semibold" style={{ color: RELATION_COLORS[relation] || DEFAULT_RELATION_COLOR }}>
                                {direction === "out" ? "→" : "←"} {relation}
                              </span>
                              <span className="text-xs text-slate-200 overflow-hidden text-ellipsis whitespace-nowrap">
                                {node.label}
                              </span>
                            </div>
                            <ArrowRight className="h-3.5 w-3.5 text-slate-500 flex-shrink-0" />
                          </div>
                        ))
                      )}
                    </div>
                  </div>
                </div>
              ) : (
                <div className="text-center py-10 px-4 text-slate-500 space-y-2">
                  <Layers className="h-8 w-8 mx-auto opacity-40" />
                  <p className="text-xs">Haz clic en cualquier nodo del grafo para inspeccionar sus atributos y relaciones.</p>
                </div>
              )}
            </div>
          )}

          {/* Node Action Buttons */}
          {selectedNode && viewMode !== "analytics" && (
            <div className="pt-4 border-t border-[var(--border-subtle)] space-y-2">
              <Button
                onClick={() => handleInspectBlastRadius(selectedNode.id)}
                variant="default"
                size="sm"
                className="w-full justify-center bg-rose-600 hover:bg-rose-500 text-white gap-1.5 shadow-md shadow-rose-600/20"
                disabled={blastLoading}
              >
                <Zap className="h-3.5 w-3.5 text-amber-300" />
                <span>Analizar Blast Radius (Impacto)</span>
              </Button>

              <Button
                onClick={handleExpandNode}
                variant="secondary"
                size="sm"
                className="w-full justify-center"
                disabled={loading}
              >
                <Plus className="h-3.5 w-3.5 text-blue-400" />
                <span>Expandir Vecinos en este Nodo</span>
              </Button>

              <Button
                onClick={() => {
                  setTargetObsId("");
                  setIsConnectModalOpen(true);
                }}
                variant="secondary"
                size="sm"
                className="w-full justify-center"
              >
                <LinkIcon className="h-3.5 w-3.5 text-purple-400" />
                <span>Crear Conexión / Arista</span>
              </Button>

              <Button
                onClick={() => {
                  setObsoleteObsId("");
                  setIsResolveModalOpen(true);
                }}
                variant="secondary"
                size="sm"
                className="w-full justify-center"
              >
                <AlertTriangle className="h-3.5 w-3.5 text-amber-500" />
                <span>Resolver Conflicto / Supersede</span>
              </Button>
            </div>
          )}
        </Card>
      </div>

      {/* Modal 1: Connect / Create Edge */}
      <Dialog open={isConnectModalOpen && !!selectedNode} onOpenChange={setIsConnectModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <LinkIcon className="h-4 w-4 text-purple-400" />
            Crear Conexión Semántica en el Grafo
          </DialogTitle>
          <DialogClose onClick={() => setIsConnectModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleCreateEdge} className="space-y-3.5 mt-4 text-xs">
          <p className="text-slate-400">
            Crea una nueva arista dirigida desde <b className="text-white">{selectedNode?.label}</b> hacia otra observación.
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              OBSERVACIÓN DESTINO (TARGET)
            </label>
            <Select
              value={targetObsId}
              onChange={(e) => setTargetObsId(e.target.value)}
              required
            >
              <option value="">Selecciona la observación destino...</option>
              {observations
                .filter((o) => o.id !== selectedNode?.id)
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.title} ({o.project})
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
            >
              <option value="relates_to">relates_to (Relación general)</option>
              <option value="references">references (Referencia técnica)</option>
              <option value="follows">follows (Secuencia temporal / lógica)</option>
              <option value="supersedes">supersedes (Reemplaza / actualiza)</option>
              <option value="contradicts">contradicts (Conflicto directo)</option>
              <option value="caused_by">caused_by (Causa / origen)</option>
              <option value="calls">calls (Llamada de función / método)</option>
              <option value="imports">imports (Importación de módulo)</option>
              <option value="implements">implements (Implementa interfaz)</option>
              <option value="defines">defines (Declara símbolo)</option>
              <option value="uses">uses (Uso de tipo / variable)</option>
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              JUSTIFICACIÓN O MOTIVO (OPCIONAL)
            </label>
            <Input
              type="text"
              value={relationReason}
              onChange={(e) => setRelationReason(e.target.value)}
              placeholder="Ej: Dependencia directa descubierta en la sesión de pruebas"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsConnectModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isConnecting}>
              {isConnecting ? "Conectando..." : "Crear Conexión"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Modal 2: Resolve Conflict / Supersede */}
      <Dialog open={isResolveModalOpen && !!selectedNode} onOpenChange={setIsResolveModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <AlertTriangle className="h-4 w-4 text-amber-500" />
            Resolución Dinámica de Conflictos
          </DialogTitle>
          <DialogClose onClick={() => setIsResolveModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleResolveConflict} className="space-y-3.5 mt-4 text-xs">
          <p className="text-slate-400">
            Marca la observación <b className="text-white">{selectedNode?.label}</b> como la versión vigente que <b className="text-emerald-400">supera o invalida</b> a una observación previa.
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              OBSERVACIÓN OBSOLETA A REEMPLAZAR
            </label>
            <Select
              value={obsoleteObsId}
              onChange={(e) => setObsoleteObsId(e.target.value)}
              required
            >
              <option value="">Selecciona la observación obsoleta...</option>
              {observations
                .filter((o) => o.id !== selectedNode?.id)
                .map((o) => (
                  <option key={o.id} value={o.id}>
                    {o.title} ({o.project})
                  </option>
                ))}
            </Select>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              MOTIVO / JUSTIFICACIÓN DEL CAMBIO
            </label>
            <Input
              type="text"
              value={resolveReason}
              onChange={(e) => setResolveReason(e.target.value)}
              placeholder="Ej: Migración de arquitectura aprobada en sesión de diseño"
              required
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsResolveModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isResolving}>
              {isResolving ? "Resolviendo..." : "Aplicar Resolución"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}
