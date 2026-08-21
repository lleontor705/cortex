"use client";

import React, { useEffect, useRef, useState, useCallback, useMemo } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation, GraphSubgraph, GraphNode, GraphLink } from "@/lib/api";
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
};

const DEFAULT_RELATION_COLOR = "#64748b";

const KIND_COLORS: Record<string, { bg: string; border: string; text: string; variant: "default" | "destructive" | "success" | "warning" | "purple" | "secondary" }> = {
  decision: { bg: "#1e3a8a", border: "#3b82f6", text: "#93c5fd", variant: "default" },
  bugfix: { bg: "#7f1d1d", border: "#ef4444", text: "#fca5a5", variant: "destructive" },
  pattern: { bg: "#064e3b", border: "#10b981", text: "#6ee7b7", variant: "success" },
  discovery: { bg: "#78350f", border: "#f59e0b", text: "#fcd34d", variant: "warning" },
  learning: { bg: "#4c1d95", border: "#8b5cf6", text: "#c4b5fd", variant: "purple" },
  observation: { bg: "#1e293b", border: "#475569", text: "#cbd5e1", variant: "secondary" },
  session: { bg: "#134e4a", border: "#14b8a6", text: "#5eead4", variant: "success" },
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
        console.error("Failed to load subgraph", err);
        setError(err.message || "Error al cargar el subgrafo");
      } finally {
        setLoading(false);
      }
    },
    [client, depth, maxNodes]
  );

  useEffect(() => {
    if (selectedObsId) {
      loadSubgraph(selectedObsId, depth, maxNodes);
    }
  }, [selectedObsId, depth, maxNodes, loadSubgraph]);

  // Physics Simulation Step
  const stepSimulation = useCallback(() => {
    if (alphaRef.current < 0.005) {
      alphaRef.current = 0.005;
    } else {
      alphaRef.current *= 0.985;
    }

    const alpha = alphaRef.current;
    const nodes = nodesRef.current;
    const edges = edgesRef.current;
    const centerX = 400;
    const centerY = 300;

    // 1. Center Gravity Force
    for (const node of nodes) {
      if (node.isPinned) continue;
      const dx = centerX - node.x;
      const dy = centerY - node.y;
      node.vx += dx * 0.001 * alpha;
      node.vy += dy * 0.001 * alpha;
    }

    // 2. Node Repulsion
    for (let i = 0; i < nodes.length; i++) {
      const a = nodes[i];
      for (let j = i + 1; j < nodes.length; j++) {
        const b = nodes[j];
        let dx = b.x - a.x;
        let dy = b.y - a.y;
        let distSq = dx * dx + dy * dy;
        if (distSq === 0) {
          dx = (Math.random() - 0.5) * 2;
          dy = (Math.random() - 0.5) * 2;
          distSq = dx * dx + dy * dy;
        }
        const dist = Math.sqrt(distSq);
        const minDist = a.radius + b.radius + 35;

        const repForce = dist < minDist ? (minDist - dist) * 0.2 : (3500 / (distSq + 200)) * alpha;
        const fx = (dx / dist) * repForce;
        const fy = (dy / dist) * repForce;

        if (!a.isPinned) {
          a.vx -= fx;
          a.vy -= fy;
        }
        if (!b.isPinned) {
          b.vx += fx;
          b.vy += fy;
        }
      }
    }

    // 3. Link Spring Attraction
    for (const edge of edges) {
      const a = edge.sourceNode;
      const b = edge.targetNode;
      if (!a || !b) continue;

      const dx = b.x - a.x;
      const dy = b.y - a.y;
      const dist = Math.sqrt(dx * dx + dy * dy) || 1;
      const targetDist = 110;
      const springForce = (dist - targetDist) * 0.04 * alpha;

      const fx = (dx / dist) * springForce;
      const fy = (dy / dist) * springForce;

      if (!a.isPinned) {
        a.vx += fx;
        a.vy += fy;
      }
      if (!b.isPinned) {
        b.vx += fx;
        b.vy += fy;
      }
    }

    // 4. Update Positions
    for (const node of nodes) {
      if (node.isPinned) {
        node.vx = 0;
        node.vy = 0;
        continue;
      }
      node.vx *= 0.82;
      node.vy *= 0.82;
      node.x += node.vx;
      node.y += node.vy;
    }
  }, []);

  // Main Canvas & Mini-Map Rendering
  const renderCanvas = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    const dpr = window.devicePixelRatio || 1;
    const width = canvas.clientWidth;
    const height = canvas.clientHeight;
    if (canvas.width !== width * dpr || canvas.height !== height * dpr) {
      canvas.width = width * dpr;
      canvas.height = height * dpr;
    }

    ctx.save();
    ctx.scale(dpr, dpr);
    ctx.clearRect(0, 0, width, height);

    // Background Grid
    ctx.save();
    ctx.strokeStyle = "rgba(30, 41, 59, 0.4)";
    ctx.lineWidth = 1;
    const gridSize = 40 * zoom;
    const startX = (offset.x % gridSize) - gridSize;
    const startY = (offset.y % gridSize) - gridSize;
    ctx.beginPath();
    for (let x = startX; x < width + gridSize; x += gridSize) {
      ctx.moveTo(x, 0);
      ctx.lineTo(x, height);
    }
    for (let y = startY; y < height + gridSize; y += gridSize) {
      ctx.moveTo(0, y);
      ctx.lineTo(width, y);
    }
    ctx.stroke();
    ctx.restore();

    // World Space Transform
    ctx.save();
    ctx.translate(offset.x, offset.y);
    ctx.scale(zoom, zoom);

    const nodes = nodesRef.current;
    const edges = edgesRef.current;
    const rootId = rootIdRef.current;

    const visibleEdges = edges.filter((e) => activeFilters[e.type] !== false);

    const activeNode = hoveredNode || selectedNode;
    const connectedNodeIds = new Set<string>();
    if (activeNode) {
      connectedNodeIds.add(activeNode.id);
      visibleEdges.forEach((e) => {
        if (e.source === activeNode.id) connectedNodeIds.add(e.target);
        if (e.target === activeNode.id) connectedNodeIds.add(e.source);
      });
    }

    // 1. Draw Edges
    for (const edge of visibleEdges) {
      const from = edge.sourceNode;
      const to = edge.targetNode;
      if (!from || !to) continue;

      const isConnected = activeNode ? connectedNodeIds.has(from.id) && connectedNodeIds.has(to.id) : true;
      const baseColor = RELATION_COLORS[edge.type] || DEFAULT_RELATION_COLOR;

      ctx.save();
      ctx.beginPath();
      ctx.moveTo(from.x, from.y);

      const midX = (from.x + to.x) / 2;
      const midY = (from.y + to.y) / 2;
      ctx.lineTo(to.x, to.y);

      ctx.strokeStyle = isConnected ? baseColor : "rgba(51, 65, 85, 0.35)";
      ctx.lineWidth = isConnected ? (edge.type === "contradicts" || edge.type === "supersedes" ? 3 : 2) : 1;

      if (edge.type === "contradicts" && isConnected) {
        ctx.setLineDash([6, 4]);
      } else if (edge.type === "references" && isConnected) {
        ctx.setLineDash([4, 2]);
      }

      ctx.stroke();
      ctx.setLineDash([]);

      // Directional Arrowhead
      const dx = to.x - from.x;
      const dy = to.y - from.y;
      const angle = Math.atan2(dy, dx);
      const arrowDist = to.radius + 4;
      const arrowX = to.x - Math.cos(angle) * arrowDist;
      const arrowY = to.y - Math.sin(angle) * arrowDist;
      const arrowSize = isConnected ? 8 : 6;

      ctx.fillStyle = isConnected ? baseColor : "rgba(71, 85, 105, 0.4)";
      ctx.beginPath();
      ctx.moveTo(arrowX, arrowY);
      ctx.lineTo(
        arrowX - arrowSize * Math.cos(angle - Math.PI / 6),
        arrowY - arrowSize * Math.sin(angle - Math.PI / 6)
      );
      ctx.lineTo(
        arrowX - arrowSize * Math.cos(angle + Math.PI / 6),
        arrowY - arrowSize * Math.sin(angle + Math.PI / 6)
      );
      ctx.closePath();
      ctx.fill();

      // Relation Type Badge on Edge
      if (isConnected && zoom >= 0.7) {
        ctx.fillStyle = "rgba(15, 23, 42, 0.85)";
        ctx.fillRect(midX - 25, midY - 9, 50, 18);
        ctx.strokeStyle = baseColor;
        ctx.lineWidth = 1;
        ctx.strokeRect(midX - 25, midY - 9, 50, 18);

        ctx.fillStyle = isConnected ? baseColor : "#94a3b8";
        ctx.font = "bold 9px Inter, sans-serif";
        ctx.textAlign = "center";
        ctx.textBaseline = "middle";
        ctx.fillText(edge.type, midX, midY);
      }
      ctx.restore();
    }

    // 2. Draw Nodes
    for (const node of nodes) {
      const isRoot = node.id === rootId;
      const isSelected = selectedNode?.id === node.id;
      const isSearchMatch = searchQuery && node.label.toLowerCase().includes(searchQuery.toLowerCase());
      const isDimmed = activeNode ? !connectedNodeIds.has(node.id) : false;

      const kindStyle = KIND_COLORS[node.kind] || KIND_COLORS.observation;

      ctx.save();
      if (isDimmed && !isSearchMatch) {
        ctx.globalAlpha = 0.25;
      }

      // Outer Glow
      if (isRoot || isSelected || isSearchMatch) {
        ctx.beginPath();
        ctx.arc(node.x, node.y, node.radius + (isSelected ? 8 : 5), 0, 2 * Math.PI);
        ctx.fillStyle = isRoot
          ? "rgba(59, 130, 246, 0.2)"
          : isSearchMatch
          ? "rgba(245, 158, 11, 0.25)"
          : "rgba(96, 165, 250, 0.25)";
        ctx.fill();
      }

      // Node Body
      ctx.beginPath();
      ctx.arc(node.x, node.y, node.radius, 0, 2 * Math.PI);
      ctx.fillStyle = isRoot ? "#2563eb" : isSelected ? "#3b82f6" : kindStyle.bg;
      ctx.fill();

      // Node Border
      ctx.lineWidth = isSelected ? 3.5 : isRoot ? 2.5 : 1.5;
      ctx.strokeStyle = isSelected
        ? "#93c5fd"
        : isRoot
        ? "#60a5fa"
        : isSearchMatch
        ? "#f59e0b"
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
      ctx.font = isRoot
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
        ctx.fillStyle = kindStyle.text;
        ctx.font = "9px Inter, sans-serif";
        ctx.fillText(`[${node.kind}]`, node.x, node.y + node.radius + 20);
      }

      ctx.restore();
    }

    ctx.restore();

    // 3. Mini-Map
    renderMinimap();
  }, [zoom, offset, selectedNode, hoveredNode, searchQuery, activeFilters]);

  // Mini-Map
  const renderMinimap = () => {
    const minimap = minimapRef.current;
    const mainCanvas = canvasRef.current;
    if (!minimap || !mainCanvas) return;
    const mctx = minimap.getContext("2d");
    if (!mctx) return;

    const mw = minimap.width;
    const mh = minimap.height;
    mctx.clearRect(0, 0, mw, mh);

    const nodes = nodesRef.current;
    if (nodes.length === 0) return;

    let minX = Infinity,
      maxX = -Infinity,
      minY = Infinity,
      maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    }
    const padding = 80;
    minX -= padding;
    minY -= padding;
    maxX += padding;
    maxY += padding;

    const boxW = Math.max(maxX - minX, 100);
    const boxH = Math.max(maxY - minY, 100);
    const scale = Math.min(mw / boxW, mh / boxH);

    mctx.save();
    mctx.translate((mw - boxW * scale) / 2, (mh - boxH * scale) / 2);
    mctx.scale(scale, scale);
    mctx.translate(-minX, -minY);

    for (const n of nodes) {
      mctx.beginPath();
      mctx.arc(n.x, n.y, n.radius * 0.8, 0, 2 * Math.PI);
      mctx.fillStyle = n.id === rootIdRef.current ? "#3b82f6" : "#64748b";
      mctx.fill();
    }

    const viewLeft = -offset.x / zoom;
    const viewTop = -offset.y / zoom;
    const viewWidth = mainCanvas.clientWidth / zoom;
    const viewHeight = mainCanvas.clientHeight / zoom;

    mctx.strokeStyle = "rgba(59, 130, 246, 0.8)";
    mctx.lineWidth = 2 / scale;
    mctx.strokeRect(viewLeft, viewTop, viewWidth, viewHeight);

    mctx.restore();
  };

  // Loop
  useEffect(() => {
    let active = true;
    const loop = () => {
      if (!active) return;
      stepSimulation();
      renderCanvas();
      animationFrameId.current = requestAnimationFrame(loop);
    };
    loop();

    return () => {
      active = false;
      if (animationFrameId.current) {
        cancelAnimationFrame(animationFrameId.current);
      }
    };
  }, [stepSimulation, renderCanvas]);

  const handleMouseDown = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();
    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;

    for (let i = nodesRef.current.length - 1; i >= 0; i--) {
      const node = nodesRef.current[i];
      const dist = Math.hypot(mouseX - node.x, mouseY - node.y);
      if (dist <= node.radius + 5) {
        draggedNodeRef.current = node;
        node.isPinned = true;
        alphaRef.current = 0.3;
        return;
      }
    }

    isDraggingRef.current = true;
    dragStartRef.current = { x: e.clientX - offset.x, y: e.clientY - offset.y };
  };

  const handleMouseMove = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();

    if (draggedNodeRef.current) {
      const mouseX = (e.clientX - rect.left - offset.x) / zoom;
      const mouseY = (e.clientY - rect.top - offset.y) / zoom;
      draggedNodeRef.current.x = mouseX;
      draggedNodeRef.current.y = mouseY;
      draggedNodeRef.current.vx = 0;
      draggedNodeRef.current.vy = 0;
      alphaRef.current = Math.max(alphaRef.current, 0.2);
      return;
    }

    if (isDraggingRef.current) {
      setOffset({
        x: e.clientX - dragStartRef.current.x,
        y: e.clientY - dragStartRef.current.y,
      });
      return;
    }

    const mouseX = (e.clientX - rect.left - offset.x) / zoom;
    const mouseY = (e.clientY - rect.top - offset.y) / zoom;
    let foundHover: SimulationNode | null = null;
    for (let i = nodesRef.current.length - 1; i >= 0; i--) {
      const node = nodesRef.current[i];
      const dist = Math.hypot(mouseX - node.x, mouseY - node.y);
      if (dist <= node.radius + 4) {
        foundHover = node;
        break;
      }
    }
    setHoveredNode(foundHover);
  };

  const handleMouseUp = () => {
    isDraggingRef.current = false;
    draggedNodeRef.current = null;
  };

  const handleCanvasClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();
    const clickX = (e.clientX - rect.left - offset.x) / zoom;
    const clickY = (e.clientY - rect.top - offset.y) / zoom;

    for (let i = nodesRef.current.length - 1; i >= 0; i--) {
      const node = nodesRef.current[i];
      const dist = Math.hypot(clickX - node.x, clickY - node.y);
      if (dist <= node.radius + 6) {
        setSelectedNode(node);
        return;
      }
    }
  };

  const handleDoubleClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();
    const clickX = (e.clientX - rect.left - offset.x) / zoom;
    const clickY = (e.clientY - rect.top - offset.y) / zoom;

    for (const node of nodesRef.current) {
      const dist = Math.hypot(clickX - node.x, clickY - node.y);
      if (dist <= node.radius + 6) {
        node.isPinned = !node.isPinned;
        alphaRef.current = 0.4;
        return;
      }
    }
  };

  const handleWheel = (e: React.WheelEvent<HTMLCanvasElement>) => {
    e.preventDefault();
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();

    const mouseX = e.clientX - rect.left;
    const mouseY = e.clientY - rect.top;

    const zoomFactor = e.deltaY < 0 ? 1.12 : 0.88;
    const newZoom = Math.min(Math.max(zoom * zoomFactor, 0.25), 3.5);

    const newOffsetX = mouseX - (mouseX - offset.x) * (newZoom / zoom);
    const newOffsetY = mouseY - (mouseY - offset.y) * (newZoom / zoom);

    setZoom(newZoom);
    setOffset({ x: newOffsetX, y: newOffsetY });
  };

  const focusOnNode = (node: SimulationNode) => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const targetX = canvas.clientWidth / 2 - node.x * zoom;
    const targetY = canvas.clientHeight / 2 - node.y * zoom;
    setOffset({ x: targetX, y: targetY });
    setSelectedNode(node);
  };

  const handleFitView = () => {
    const canvas = canvasRef.current;
    const nodes = nodesRef.current;
    if (!canvas || nodes.length === 0) return;

    let minX = Infinity,
      maxX = -Infinity,
      minY = Infinity,
      maxY = -Infinity;
    for (const n of nodes) {
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    }

    const padding = 80;
    const graphW = Math.max(maxX - minX + padding * 2, 200);
    const graphH = Math.max(maxY - minY + padding * 2, 200);

    const fitZoom = Math.min(
      Math.max(Math.min(canvas.clientWidth / graphW, canvas.clientHeight / graphH), 0.35),
      1.6
    );
    const fitOffsetX = canvas.clientWidth / 2 - ((minX + maxX) / 2) * fitZoom;
    const fitOffsetY = canvas.clientHeight / 2 - ((minY + maxY) / 2) * fitZoom;

    setZoom(fitZoom);
    setOffset({ x: fitOffsetX, y: fitOffsetY });
  };

  const handleReheatSimulation = () => {
    nodesRef.current.forEach((n) => {
      n.vx += (Math.random() - 0.5) * 8;
      n.vy += (Math.random() - 0.5) * 8;
      n.isPinned = false;
    });
    alphaRef.current = 1.0;
  };

  const handleExpandNode = async () => {
    if (!selectedNode || !client) return;
    setLoading(true);
    try {
      const data = await client.subgraph(selectedNode.id, 1, 30);
      const existingIds = new Set(nodesRef.current.map((n) => n.id));

      const newNodes: SimulationNode[] = [];
      data.nodes.forEach((n, idx) => {
        if (!existingIds.has(n.id)) {
          const angle = (idx / (data.nodes.length || 1)) * 2 * Math.PI;
          newNodes.push({
            ...n,
            x: selectedNode.x + 120 * Math.cos(angle),
            y: selectedNode.y + 120 * Math.sin(angle),
            vx: 0,
            vy: 0,
            radius: 18,
          });
        }
      });

      const nodeMap = new Map<string, SimulationNode>();
      [...nodesRef.current, ...newNodes].forEach((n) => nodeMap.set(n.id, n));

      const existingEdgeKeys = new Set(edgesRef.current.map((e) => `${e.source}->${e.target}`));
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
            <span>Grafo de Conocimiento & Relaciones</span>
          </h1>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Explorador visual 2D interactivo a 60 FPS con motor de físicas, detección de conflictos y análisis de dependencias
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
        </div>
      </div>

      {/* Metrics Bar */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3.5 sm:gap-4">
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Nodos en el Grafo</span>
          <span className="text-2xl font-bold text-[var(--text-primary)] mt-1 block">{stats.nodes}</span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Aristas de Relación</span>
          <span className="text-2xl font-bold text-blue-400 mt-1 block">{stats.edges}</span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Conflictos Detectados</span>
          <span className={`text-2xl font-bold mt-1 block ${stats.contradicts > 0 ? "text-red-400" : "text-[var(--text-muted)]"}`}>
            {stats.contradicts}
          </span>
        </Card>
        <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">Superaciones Activas</span>
          <span className="text-2xl font-bold text-emerald-400 mt-1 block">{stats.supersedes}</span>
        </Card>
      </div>

      {/* Relation Type Filter Chips & Search Bar */}
      <Card className="p-3 bg-[var(--bg-secondary)] border-[var(--border-subtle)] flex flex-wrap items-center justify-between gap-3">
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
      <div className="grid grid-cols-1 lg:grid-cols-[1fr_360px] gap-4 sm:gap-5">
        {/* Canvas Explorer Card */}
        <Card
          ref={containerRef}
          className="relative p-0 overflow-hidden min-h-[420px] sm:min-h-[500px] h-[55vh] lg:h-[calc(100vh-340px)] flex flex-col border-[var(--border-subtle)] bg-[var(--bg-primary)]"
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

          {/* Mini-Map Radar HUD in Bottom-Right Corner */}
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

        {/* Node Inspector Drawer */}
        <Card className="flex flex-col justify-between min-h-[300px] lg:h-[calc(100vh-340px)] overflow-y-auto border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 sm:p-5">
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
                    className="flex items-center justify-between p-2 rounded-lg bg-slate-950/80 border border-slate-800 hover:border-slate-700 cursor-pointer mt-1 transition-colors"
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
                  <div className="mt-1.5 space-y-1.5 max-h-36 overflow-y-auto pr-1">
                    {selectedNodeNeighbors.length === 0 ? (
                      <p className="text-xs text-slate-500 italic">Sin conexiones directas visibles</p>
                    ) : (
                      selectedNodeNeighbors.map(({ node, relation, direction }, idx) => (
                        <div
                          key={idx}
                          onClick={() => focusOnNode(node)}
                          className="flex items-center justify-between p-2 bg-slate-800/60 rounded-lg border border-slate-700/50 hover:bg-slate-800 hover:border-slate-600 cursor-pointer transition-all"
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

          {/* Node Action Buttons */}
          {selectedNode && (
            <div className="pt-4 border-t border-slate-800 space-y-2">
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
