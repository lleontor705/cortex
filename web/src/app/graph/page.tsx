"use client";

import React, { useEffect, useRef, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation, GraphSubgraph, GraphNode, GraphLink } from "@/lib/api";
import {
  Share2,
  ZoomIn,
  ZoomOut,
  RotateCcw,
  Plus,
  AlertTriangle,
  CheckCircle,
  Layers,
  ArrowRight,
  Info,
  X,
} from "lucide-react";

export default function GraphPage() {
  const { client } = useAuth();
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  const [observations, setObservations] = useState<Observation[]>([]);
  const [selectedObsId, setSelectedObsId] = useState<string>("");
  const [subgraph, setSubgraph] = useState<GraphSubgraph | null>(null);
  const [selectedNode, setSelectedNode] = useState<GraphNode | null>(null);
  const [loading, setLoading] = useState(false);

  // Graph state for Canvas
  const [zoom, setZoom] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const [isDragging, setIsDragging] = useState(false);
  const [dragStart, setDragStart] = useState({ x: 0, y: 0 });
  const [nodePositions, setNodePositions] = useState<Map<string, { x: number; y: number }>>(new Map());

  // Conflict Resolution Modal
  const [isResolveModalOpen, setIsResolveModalOpen] = useState(false);
  const [obsoleteObsId, setObsoleteObsId] = useState("");
  const [resolveReason, setResolveReason] = useState("");
  const [isResolving, setIsResolving] = useState(false);

  // Connect Modal
  const [isConnectModalOpen, setIsConnectModalOpen] = useState(false);
  const [targetObsId, setTargetObsId] = useState("");
  const [relationType, setRelationType] = useState("relates_to");
  const [relationReason, setRelationReason] = useState("");

  useEffect(() => {
    if (!client) return;
    client.listObservations("?limit=50").then((obs) => {
      setObservations(obs || []);
      if (obs && obs.length > 0 && !selectedObsId) {
        setSelectedObsId(obs[0].id);
      }
    });
  }, [client]);

  const loadSubgraph = async (rootId: string) => {
    if (!client || !rootId) return;
    setLoading(true);
    try {
      const data = await client.subgraph(rootId, 2, 80);
      setSubgraph(data);

      // Compute initial circular layout positions
      const positions = new Map<string, { x: number; y: number }>();
      const count = data.nodes.length;
      const centerX = 350;
      const centerY = 250;

      positions.set(data.root, { x: centerX, y: centerY });

      data.nodes.forEach((node, idx) => {
        if (node.id === data.root) return;
        const angle = (idx / (count || 1)) * 2 * Math.PI;
        const radius = node.hop === 1 ? 160 : 260;
        positions.set(node.id, {
          x: centerX + radius * Math.cos(angle),
          y: centerY + radius * Math.sin(angle),
        });
      });

      setNodePositions(positions);
      const rootNode = data.nodes.find((n) => n.id === data.root) || null;
      setSelectedNode(rootNode);
    } catch (err) {
      console.error("Failed to load subgraph", err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (selectedObsId) {
      loadSubgraph(selectedObsId);
    }
  }, [selectedObsId]);

  // Render Canvas
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !subgraph) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.save();
    ctx.translate(offset.x, offset.y);
    ctx.scale(zoom, zoom);

    // Draw Edges
    subgraph.edges.forEach((edge) => {
      const from = nodePositions.get(edge.source);
      const to = nodePositions.get(edge.target);
      if (!from || !to) return;

      ctx.beginPath();
      ctx.moveTo(from.x, from.y);
      ctx.lineTo(to.x, to.y);

      let strokeColor = "#334155";
      if (edge.type === "contradicts") strokeColor = "#ef4444";
      else if (edge.type === "supersedes") strokeColor = "#10b981";
      else if (edge.type === "references") strokeColor = "#3b82f6";
      else if (edge.type === "follows") strokeColor = "#f59e0b";

      ctx.strokeStyle = strokeColor;
      ctx.lineWidth = edge.type === "contradicts" || edge.type === "supersedes" ? 2.5 : 1.5;
      ctx.stroke();

      // Draw relation label in middle of edge
      const midX = (from.x + to.x) / 2;
      const midY = (from.y + to.y) / 2;
      ctx.fillStyle = strokeColor;
      ctx.font = "10px Inter, sans-serif";
      ctx.fillText(edge.type, midX + 5, midY - 5);
    });

    // Draw Nodes
    subgraph.nodes.forEach((node) => {
      const pos = nodePositions.get(node.id);
      if (!pos) return;

      const isRoot = node.id === subgraph.root;
      const isSelected = selectedNode?.id === node.id;
      const radius = isRoot ? 24 : 18;

      ctx.beginPath();
      ctx.arc(pos.x, pos.y, radius, 0, 2 * Math.PI);
      ctx.fillStyle = isRoot ? "#3b82f6" : isSelected ? "#60a5fa" : "#1e293b";
      ctx.fill();
      ctx.lineWidth = isSelected ? 3 : 1.5;
      ctx.strokeStyle = isRoot ? "#93c5fd" : "#475569";
      ctx.stroke();

      // Node label
      ctx.fillStyle = "#f8fafc";
      ctx.font = isRoot ? "bold 12px Inter, sans-serif" : "11px Inter, sans-serif";
      ctx.textAlign = "center";
      const shortLabel = node.label.length > 20 ? node.label.slice(0, 18) + "..." : node.label;
      ctx.fillText(shortLabel, pos.x, pos.y + radius + 14);
    });

    ctx.restore();
  }, [subgraph, nodePositions, zoom, offset, selectedNode]);

  // Handle Canvas Click / Selection
  const handleCanvasClick = (e: React.MouseEvent<HTMLCanvasElement>) => {
    const canvas = canvasRef.current;
    if (!canvas || !subgraph) return;
    const rect = canvas.getBoundingClientRect();
    const clickX = (e.clientX - rect.left - offset.x) / zoom;
    const clickY = (e.clientY - rect.top - offset.y) / zoom;

    for (const node of subgraph.nodes) {
      const pos = nodePositions.get(node.id);
      if (pos) {
        const dist = Math.hypot(clickX - pos.x, clickY - pos.y);
        if (dist <= 24) {
          setSelectedNode(node);
          return;
        }
      }
    }
  };

  const handleMouseDown = (e: React.MouseEvent) => {
    setIsDragging(true);
    setDragStart({ x: e.clientX - offset.x, y: e.clientY - offset.y });
  };

  const handleMouseMove = (e: React.MouseEvent) => {
    if (!isDragging) return;
    setOffset({ x: e.clientX - dragStart.x, y: e.clientY - dragStart.y });
  };

  const handleMouseUp = () => setIsDragging(false);

  const handleResolveConflict = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !selectedNode || !obsoleteObsId) return;
    setIsResolving(true);
    try {
      await client.resolveConflict({
        new_observation_id: selectedNode.id,
        obsolete_observation_id: obsoleteObsId,
        reason: resolveReason || "Reemplazado por conocimiento más reciente",
      });
      setIsResolveModalOpen(false);
      setResolveReason("");
      loadSubgraph(selectedNode.id);
      alert("¡Conflicto resuelto exitosamente! Arista 'supersedes' creada.");
    } catch (err: any) {
      alert("Error al resolver: " + (err.message || err));
    } finally {
      setIsResolving(false);
    }
  };

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "20px", flexWrap: "wrap", gap: "16px" }}>
        <div>
          <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
            Grafo de Conocimiento & Relaciones
          </h1>
          <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
            Explorador visual 2D de relaciones semánticas, dependencias y resolución de conflictos entre observaciones
          </p>
        </div>

        <div style={{ display: "flex", gap: "10px", alignItems: "center" }}>
          <select
            className="select"
            value={selectedObsId}
            onChange={(e) => setSelectedObsId(e.target.value)}
            style={{ width: "240px" }}
          >
            {observations.map((o) => (
              <option key={o.id} value={o.id}>
                {o.title}
              </option>
            ))}
          </select>

          <button
            onClick={() => {
              setZoom(1);
              setOffset({ x: 0, y: 0 });
            }}
            className="btn btn-secondary btn-sm"
            title="Resetear vista"
          >
            <RotateCcw size={14} />
          </button>
        </div>
      </div>

      {/* Main Graph Grid */}
      <div style={{ display: "grid", gridTemplateColumns: "1fr 340px", gap: "20px" }}>
        {/* Interactive Canvas */}
        <div className="card" style={{ position: "relative", padding: 0, overflow: "hidden", minHeight: "540px", display: "flex", flexDirection: "column" }}>
          {/* Controls Overlay */}
          <div style={{ position: "absolute", top: "14px", left: "14px", display: "flex", gap: "8px", zIndex: 10 }}>
            <button onClick={() => setZoom((z) => Math.min(z + 0.2, 2.5))} className="btn btn-secondary btn-sm">
              <ZoomIn size={14} />
            </button>
            <button onClick={() => setZoom((z) => Math.max(z - 0.2, 0.4))} className="btn btn-secondary btn-sm">
              <ZoomOut size={14} />
            </button>
          </div>

          {/* Legend Overlay */}
          <div style={{ position: "absolute", bottom: "14px", left: "14px", display: "flex", gap: "10px", flexWrap: "wrap", zIndex: 10, background: "rgba(15, 23, 42, 0.8)", backdropFilter: "blur(8px)", padding: "8px 12px", borderRadius: "var(--radius-md)", border: "1px solid var(--border-subtle)", fontSize: "11px" }}>
            <span style={{ color: "#60a5fa" }}>● Raíz</span>
            <span style={{ color: "#3b82f6" }}>— references</span>
            <span style={{ color: "#10b981" }}>— supersedes</span>
            <span style={{ color: "#ef4444" }}>— contradicts</span>
            <span style={{ color: "#f59e0b" }}>— follows</span>
          </div>

          <canvas
            ref={canvasRef}
            width={800}
            height={540}
            onClick={handleCanvasClick}
            onMouseDown={handleMouseDown}
            onMouseMove={handleMouseMove}
            onMouseUp={handleMouseUp}
            style={{ width: "100%", height: "100%", cursor: isDragging ? "grabbing" : "grab", display: "block" }}
          />
        </div>

        {/* Node Inspector Drawer */}
        <div className="card" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between" }}>
          <div>
            <div className="card-header">
              <h2 className="card-title">
                <Info size={16} />
                Detalle del Nodo
              </h2>
            </div>

            {selectedNode ? (
              <div style={{ display: "flex", flexDirection: "column", gap: "12px", fontSize: "13px" }}>
                <div>
                  <span style={{ color: "var(--text-muted)", fontSize: "11px", textTransform: "uppercase" }}>Título</span>
                  <div style={{ fontWeight: "600", color: "var(--text-primary)", marginTop: "2px" }}>
                    {selectedNode.label}
                  </div>
                </div>

                <div>
                  <span style={{ color: "var(--text-muted)", fontSize: "11px", textTransform: "uppercase" }}>Tipo de Entidad</span>
                  <div style={{ marginTop: "2px" }}>
                    <span className="badge badge-blue">{selectedNode.kind}</span>
                  </div>
                </div>

                <div>
                  <span style={{ color: "var(--text-muted)", fontSize: "11px", textTransform: "uppercase" }}>ID Opaque</span>
                  <div className="font-mono" style={{ fontSize: "11px", color: "var(--text-secondary)", marginTop: "2px" }}>
                    {selectedNode.id}
                  </div>
                </div>

                <div>
                  <span style={{ color: "var(--text-muted)", fontSize: "11px", textTransform: "uppercase" }}>Distancia de Saltos (Hop)</span>
                  <div style={{ color: "var(--text-primary)", marginTop: "2px" }}>
                    {selectedNode.hop === 0 ? "Nodo Central (0)" : `${selectedNode.hop} salto(s)`}
                  </div>
                </div>
              </div>
            ) : (
              <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>
                Haz clic en cualquier nodo del grafo para inspeccionar sus atributos.
              </p>
            )}
          </div>

          {selectedNode && (
            <div style={{ marginTop: "20px", display: "flex", flexDirection: "column", gap: "8px" }}>
              <button
                onClick={() => setIsResolveModalOpen(true)}
                className="btn btn-secondary"
                style={{ width: "100%", justifyContent: "center" }}
              >
                <AlertTriangle size={14} color="#f59e0b" />
                <span>Resolver Conflicto / Supersede</span>
              </button>
            </div>
          )}
        </div>
      </div>

      {/* Resolve Conflict Modal */}
      {isResolveModalOpen && selectedNode && (
        <div style={{ position: "fixed", inset: 0, backgroundColor: "rgba(0,0,0,0.7)", backdropFilter: "blur(4px)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: "20px" }}>
          <div className="card" style={{ maxWidth: "500px", width: "100%" }}>
            <div className="card-header">
              <h2 className="card-title">
                <AlertTriangle size={18} color="#f59e0b" />
                Resolución Dinámica de Conflictos
              </h2>
              <button onClick={() => setIsResolveModalOpen(false)} className="btn btn-secondary btn-sm" style={{ padding: "4px" }}>
                <X size={16} />
              </button>
            </div>

            <form onSubmit={handleResolveConflict} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <p style={{ fontSize: "13px", color: "var(--text-secondary)" }}>
                Marca la observación <b>{selectedNode.label}</b> como la versión vigente que <b>supera o invalida</b> a una observación anterior.
              </p>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  OBSERVACIÓN OBSOLETA A REEMPLAZAR
                </label>
                <select
                  className="select"
                  value={obsoleteObsId}
                  onChange={(e) => setObsoleteObsId(e.target.value)}
                  required
                >
                  <option value="">Selecciona la observación obsoleta...</option>
                  {observations
                    .filter((o) => o.id !== selectedNode.id)
                    .map((o) => (
                      <option key={o.id} value={o.id}>
                        {o.title} ({o.project})
                      </option>
                    ))}
                </select>
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  MOTIVO / JUSTIFICACIÓN DEL CAMBIO
                </label>
                <input
                  type="text"
                  className="input"
                  value={resolveReason}
                  onChange={(e) => setResolveReason(e.target.value)}
                  placeholder="Ej: Migración de arquitectura aprobada en sesión de diseño"
                  required
                />
              </div>

              <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "8px" }}>
                <button type="button" onClick={() => setIsResolveModalOpen(false)} className="btn btn-secondary">
                  Cancelar
                </button>
                <button type="submit" className="btn btn-primary" disabled={isResolving}>
                  {isResolving ? "Resolviendo..." : "Aplicar Resolución"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
