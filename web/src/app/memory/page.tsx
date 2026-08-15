"use client";

import React, { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation, Session } from "@/lib/api";
import {
  BrainCircuit,
  Plus,
  Trash2,
  Filter,
  Search,
  Check,
  Tag,
  Clock,
  Layers,
  X,
} from "lucide-react";

export default function MemoryPage() {
  const { client, stats } = useAuth();
  const [observations, setObservations] = useState<Observation[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [filterProject, setFilterProject] = useState("");
  const [filterType, setFilterType] = useState("");
  const [searchQuery, setSearchQuery] = useState("");

  // Create Modal State
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [newTitle, setNewTitle] = useState("");
  const [newContent, setNewContent] = useState("");
  const [newType, setNewType] = useState("decision");
  const [newProject, setNewProject] = useState("default");
  const [newScope, setNewScope] = useState("project");
  const [newTags, setNewTags] = useState("");
  const [isCreating, setIsCreating] = useState(false);

  const fetchObservations = async () => {
    if (!client) return;
    setLoading(true);
    try {
      let params = "?limit=100";
      if (filterProject) params += `&project=${encodeURIComponent(filterProject)}`;
      if (filterType) params += `&type=${encodeURIComponent(filterType)}`;
      const [obsList, sessList] = await Promise.all([
        client.listObservations(params),
        client.sessions(),
      ]);
      setObservations(obsList || []);
      setSessions(sessList || []);
    } catch (err) {
      console.error("Failed to load memory data", err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchObservations();
  }, [client, filterProject, filterType]);

  const handleCreateObservation = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client) return;
    setIsCreating(true);
    try {
      const tagsArray = newTags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean);

      await client.createObservation({
        title: newTitle,
        content: newContent,
        type: newType,
        project: newProject,
        scope: newScope,
        tags: tagsArray,
        confidence: 1.0,
      });

      setIsModalOpen(false);
      setNewTitle("");
      setNewContent("");
      setNewTags("");
      fetchObservations();
    } catch (err: any) {
      alert("Error al guardar: " + (err.message || err));
    } finally {
      setIsCreating(false);
    }
  };

  const handleDeleteObservation = async (id: string) => {
    if (!confirm("¿Seguro que deseas eliminar esta observación?")) return;
    if (!client) return;
    try {
      await client.deleteObservation(id);
      setObservations((prev) => prev.filter((o) => o.id !== id));
    } catch (err: any) {
      alert("Error al eliminar: " + (err.message || err));
    }
  };

  const filteredObservations = observations.filter((obs) => {
    if (!searchQuery) return true;
    const q = searchQuery.toLowerCase();
    return (
      obs.title.toLowerCase().includes(q) ||
      obs.content.toLowerCase().includes(q) ||
      obs.project.toLowerCase().includes(q)
    );
  });

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "24px", flexWrap: "wrap", gap: "16px" }}>
        <div>
          <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
            Memoria & Observaciones
          </h1>
          <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
            Registro persistente de conocimientos, decisiones, patrones y soluciones capturadas por los agentes
          </p>
        </div>

        <button onClick={() => setIsModalOpen(true)} className="btn btn-primary">
          <Plus size={16} />
          <span>Nueva Observación</span>
        </button>
      </div>

      {/* Filter Bar */}
      <div className="card" style={{ marginBottom: "20px", padding: "16px" }}>
        <div style={{ display: "flex", gap: "14px", flexWrap: "wrap", alignItems: "center" }}>
          <div style={{ flex: 1, minWidth: "220px", position: "relative" }}>
            <input
              type="text"
              className="input"
              placeholder="Filtrar por texto o título..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
            />
          </div>

          <div style={{ minWidth: "160px" }}>
            <select
              className="select"
              value={filterType}
              onChange={(e) => setFilterType(e.target.value)}
            >
              <option value="">Todos los tipos</option>
              <option value="decision">Decisión</option>
              <option value="bugfix">Bugfix</option>
              <option value="pattern">Patrón / Convención</option>
              <option value="architecture">Arquitectura</option>
              <option value="discovery">Descubrimiento</option>
              <option value="manual">Manual / Nota</option>
            </select>
          </div>

          <div style={{ minWidth: "160px" }}>
            <input
              type="text"
              className="input"
              placeholder="Filtrar proyecto..."
              value={filterProject}
              onChange={(e) => setFilterProject(e.target.value)}
            />
          </div>
        </div>
      </div>

      {/* Observations Grid */}
      {loading ? (
        <div className="card" style={{ textAlign: "center", padding: "40px", color: "var(--text-muted)" }}>
          Cargando observaciones...
        </div>
      ) : filteredObservations.length === 0 ? (
        <div className="card" style={{ textAlign: "center", padding: "40px" }}>
          <p style={{ color: "var(--text-secondary)", marginBottom: "12px" }}>
            No se encontraron observaciones con los filtros actuales.
          </p>
          <button onClick={() => setIsModalOpen(true)} className="btn btn-secondary btn-sm">
            Crear la primera observación
          </button>
        </div>
      ) : (
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(360px, 1fr))", gap: "18px" }}>
          {filteredObservations.map((obs) => (
            <div key={obs.id} className="card" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between" }}>
              <div>
                <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: "10px", marginBottom: "8px" }}>
                  <h3 style={{ fontSize: "15px", fontWeight: "600", color: "var(--text-primary)" }}>
                    {obs.title}
                  </h3>
                  <span className={`badge ${obs.type === "decision" ? "badge-blue" : obs.type === "bugfix" ? "badge-amber" : "badge-zinc"}`}>
                    {obs.type}
                  </span>
                </div>

                <p style={{ color: "var(--text-secondary)", fontSize: "13px", lineHeight: "1.5", marginBottom: "14px", whiteSpace: "pre-wrap" }}>
                  {obs.content}
                </p>
              </div>

              <div>
                {obs.tags && obs.tags.length > 0 && (
                  <div style={{ display: "flex", gap: "6px", flexWrap: "wrap", marginBottom: "12px" }}>
                    {obs.tags.map((tag, idx) => (
                      <span key={idx} className="badge badge-zinc" style={{ fontSize: "10px" }}>
                        #{tag}
                      </span>
                    ))}
                  </div>
                )}

                <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", paddingTop: "12px", borderTop: "1px solid var(--border-subtle)", fontSize: "11px", color: "var(--text-muted)" }}>
                  <div>
                    <span>Proyecto: <b>{obs.project}</b></span>
                  </div>
                  <button
                    onClick={() => handleDeleteObservation(obs.id)}
                    className="btn btn-danger btn-sm"
                    title="Eliminar observación"
                  >
                    <Trash2 size={12} />
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Create Observation Modal */}
      {isModalOpen && (
        <div
          style={{
            position: "fixed",
            inset: 0,
            backgroundColor: "rgba(0, 0, 0, 0.7)",
            backdropFilter: "blur(4px)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 50,
            padding: "20px",
          }}
        >
          <div className="card" style={{ maxWidth: "560px", width: "100%", maxHeight: "90vh", overflowY: "auto" }}>
            <div className="card-header">
              <h2 className="card-title">
                <BrainCircuit size={18} />
                Registrar Nueva Observación
              </h2>
              <button
                onClick={() => setIsModalOpen(false)}
                className="btn btn-secondary btn-sm"
                style={{ padding: "4px" }}
              >
                <X size={16} />
              </button>
            </div>

            <form onSubmit={handleCreateObservation} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  TÍTULO / RESUMEN
                </label>
                <input
                  type="text"
                  className="input"
                  value={newTitle}
                  onChange={(e) => setNewTitle(e.target.value)}
                  placeholder="Ej: Migración de PostgreSQL a ClickHouse para analítica"
                  required
                />
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  CONTENIDO / CONTEXTO DETALLADO
                </label>
                <textarea
                  className="textarea"
                  rows={4}
                  value={newContent}
                  onChange={(e) => setNewContent(e.target.value)}
                  placeholder="Describe la decisión, causa raíz de un bug, o regla de arquitectura..."
                  required
                />
              </div>

              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "12px" }}>
                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                    TIPO
                  </label>
                  <select
                    className="select"
                    value={newType}
                    onChange={(e) => setNewType(e.target.value)}
                  >
                    <option value="decision">Decisión</option>
                    <option value="bugfix">Bugfix</option>
                    <option value="pattern">Patrón / Convención</option>
                    <option value="architecture">Arquitectura</option>
                    <option value="discovery">Descubrimiento</option>
                    <option value="manual">Manual / Nota</option>
                  </select>
                </div>

                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                    PROYECTO
                  </label>
                  <input
                    type="text"
                    className="input"
                    value={newProject}
                    onChange={(e) => setNewProject(e.target.value)}
                    required
                  />
                </div>
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  TAGS (SEPARADOS POR COMA)
                </label>
                <input
                  type="text"
                  className="input"
                  value={newTags}
                  onChange={(e) => setNewTags(e.target.value)}
                  placeholder="db, postgres, schema, performance"
                />
              </div>

              <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "12px" }}>
                <button
                  type="button"
                  onClick={() => setIsModalOpen(false)}
                  className="btn btn-secondary"
                >
                  Cancelar
                </button>
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={isCreating}
                >
                  {isCreating ? "Guardando..." : "Guardar Observación"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
