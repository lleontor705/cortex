"use client";

import React, { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation, Session } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Dialog, DialogHeader, DialogTitle, DialogClose } from "@/components/ui/dialog";
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
  Eye,
  Copy,
  ExternalLink,
  FileText,
  Calendar,
  Shield,
  Folder,
  Sparkles,
  CheckCircle2,
  AlertCircle,
  Database,
  Cpu,
} from "lucide-react";

export default function MemoryPage() {
  const { client, principal } = useAuth();
  const [observations, setObservations] = useState<Observation[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);
  const [filterProject, setFilterProject] = useState("");
  const [filterType, setFilterType] = useState("");
  const [filterRAG, setFilterRAG] = useState("");
  const [searchQuery, setSearchQuery] = useState("");

  // Detail Modal State
  const [selectedObservation, setSelectedObservation] = useState<Observation | null>(null);
  const [copiedContent, setCopiedContent] = useState(false);

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

  const userRoles = principal?.roles || ["admin"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );
  const currentSubject = principal?.id || "";
  const canDeleteObservation = (obs: Observation) => {
    if (isAdmin) return true;
    if (!obs.owner_subject || !currentSubject) return false;
    return obs.owner_subject === currentSubject || (!!principal?.email && obs.owner_subject === principal.email);
  };

  const handleDeleteObservation = async (obs: Observation) => {
    if (!canDeleteObservation(obs)) {
      alert("Acceso denegado: Solo puedes eliminar observaciones subidas por tu propio token.");
      return;
    }
    if (!confirm("¿Seguro que deseas eliminar esta observación?")) return;
    if (!client) return;
    try {
      await client.deleteObservation(obs.id);
      setObservations((prev) => prev.filter((o) => o.id !== obs.id));
    } catch (err: any) {
      alert("Error al eliminar: " + (err.message || err));
    }
  };

  const filteredObservations = observations.filter((obs) => {
    if (filterRAG === "indexed") {
      if (obs.rag_status !== "indexed" && obs.has_embedding !== true && obs.rag_status !== undefined) return false;
    } else if (filterRAG === "pending") {
      if (obs.rag_status !== "pending") return false;
    } else if (filterRAG === "unindexed") {
      if (obs.rag_status !== "unindexed" && obs.rag_status !== "failed") return false;
    }

    if (!searchQuery) return true;
    const q = searchQuery.toLowerCase();
    return (
      obs.title.toLowerCase().includes(q) ||
      obs.content.toLowerCase().includes(q) ||
      obs.project.toLowerCase().includes(q)
    );
  });

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-[var(--text-primary)] flex items-center gap-2.5">
            <BrainCircuit className="h-6 w-6 text-blue-500" />
            Memoria & Observaciones
          </h1>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Registro persistente de conocimientos, decisiones, patrones y soluciones capturadas por los agentes
          </p>
        </div>

        {isAdmin ? (
          <Button onClick={() => setIsModalOpen(true)} size="sm" className="gap-2">
            <Plus className="h-4 w-4" />
            <span>Nueva Observación Manual (Admin)</span>
          </Button>
        ) : (
          <Badge variant="secondary" className="text-xs py-1.5 px-3">
            🤖 Sincronización automática vía Agente (MCP)
          </Badge>
        )}
      </div>

      {/* Filter Bar */}
      <Card className="p-3.5 sm:p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
        <div className="flex flex-col sm:flex-row flex-wrap items-stretch sm:items-center gap-2.5 sm:gap-3">
          <div className="flex-1 min-w-[200px]">
            <Input
              type="text"
              placeholder="Filtrar por texto o título..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-9 text-xs"
            />
          </div>

          <div className="w-full sm:w-44">
            <Select
              value={filterType}
              onChange={(e) => setFilterType(e.target.value)}
              className="h-9 text-xs w-full"
            >
              <option value="">Todos los tipos</option>
              <option value="decision">Decisión</option>
              <option value="bugfix">Bugfix</option>
              <option value="pattern">Patrón / Convención</option>
              <option value="architecture">Arquitectura</option>
              <option value="discovery">Descubrimiento</option>
              <option value="manual">Manual / Nota</option>
            </Select>
          </div>

          <div className="w-full sm:w-44">
            <Select
              value={filterRAG}
              onChange={(e) => setFilterRAG(e.target.value)}
              className="h-9 text-xs w-full"
            >
              <option value="">Todos los estados RAG</option>
              <option value="indexed">🟢 Vectorizado en RAG</option>
              <option value="pending">🟡 RAG En Cola</option>
              <option value="unindexed">⚪ Sin Vectorizar</option>
            </Select>
          </div>

          <div className="w-full sm:w-44">
            <Input
              type="text"
              placeholder="Filtrar proyecto..."
              value={filterProject}
              onChange={(e) => setFilterProject(e.target.value)}
              className="h-9 text-xs"
            />
          </div>
        </div>
      </Card>

      {/* Observations Grid */}
      {loading ? (
        <Card className="p-12 text-center text-xs text-[var(--text-muted)] bg-[var(--bg-secondary)] border-[var(--border-subtle)]">
          Cargando observaciones...
        </Card>
      ) : filteredObservations.length === 0 ? (
        <Card className="p-12 text-center bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-3">
          <p className="text-xs text-[var(--text-muted)]">
            No se encontraron observaciones con los filtros actuales.
          </p>
          <Button onClick={() => setIsModalOpen(true)} variant="secondary" size="sm">
            Crear la primera observación
          </Button>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3.5 sm:gap-4">
          {filteredObservations.map((obs) => (
            <Card
              key={obs.id}
              onClick={() => setSelectedObservation(obs)}
              className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] flex flex-col justify-between hover:border-blue-500/50 hover:shadow-md transition-all cursor-pointer group"
            >
              <div>
                <div className="flex items-start justify-between gap-2 mb-2">
                  <h3 className="text-xs font-semibold text-[var(--text-primary)] group-hover:text-blue-400 leading-snug line-clamp-2 transition-colors">
                    {obs.title}
                  </h3>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <Badge variant={obs.type === "decision" ? "default" : obs.type === "bugfix" ? "destructive" : "secondary"} className="text-[10px]">
                      {obs.type}
                    </Badge>
                  </div>
                </div>

                {/* RAG Status Indicator Chip */}
                <div className="mb-2 flex items-center gap-1.5">
                  {obs.rag_status === "pending" ? (
                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono bg-amber-500/10 text-amber-400 border border-amber-500/20">
                      <Clock className="h-2.5 w-2.5 animate-spin" /> RAG En Cola
                    </span>
                  ) : obs.rag_status === "failed" ? (
                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono bg-rose-500/10 text-rose-400 border border-rose-500/20">
                      <AlertCircle className="h-2.5 w-2.5" /> Error RAG
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-mono bg-emerald-500/10 text-emerald-400 border border-emerald-500/20">
                      <Sparkles className="h-2.5 w-2.5" /> RAG Vectorizado
                    </span>
                  )}
                </div>

                <p className="text-xs text-[var(--text-secondary)] line-clamp-4 leading-relaxed mb-3 whitespace-pre-wrap">
                  {obs.content}
                </p>
              </div>

              <div>
                {obs.tags && obs.tags.length > 0 && (
                  <div className="flex flex-wrap gap-1.5 mb-3">
                    {obs.tags.slice(0, 4).map((tag, idx) => (
                      <span key={idx} className="text-[10px] px-2 py-0.5 rounded-md bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-[var(--text-muted)] font-mono">
                        #{tag}
                      </span>
                    ))}
                    {obs.tags.length > 4 && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded-md bg-[var(--bg-surface)] text-[var(--text-muted)] font-mono">
                        +{obs.tags.length - 4}
                      </span>
                    )}
                  </div>
                )}

                <div className="flex items-center justify-between pt-3 border-t border-[var(--border-subtle)] text-[11px] text-[var(--text-muted)]">
                  <div className="overflow-hidden mr-2 flex items-center gap-1.5">
                    <Folder className="h-3 w-3 text-blue-400 shrink-0" />
                    <span className="truncate">Proyecto: <b className="text-[var(--text-primary)]">{obs.project}</b></span>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      onClick={(e) => {
                        e.stopPropagation();
                        setSelectedObservation(obs);
                      }}
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-blue-400 hover:text-blue-300 hover:bg-blue-500/10"
                      title="Ver detalles completos"
                    >
                      <Eye className="h-3.5 w-3.5" />
                    </Button>
                    {canDeleteObservation(obs) ? (
                      <Button
                        onClick={(e) => {
                          e.stopPropagation();
                          handleDeleteObservation(obs);
                        }}
                        variant="ghost"
                        size="icon"
                        className="h-7 w-7 text-[var(--text-muted)] hover:text-red-400 hover:bg-red-500/10"
                        title="Eliminar observación (propietario / admin)"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </Button>
                    ) : (
                      <span className="text-[10px] text-[var(--text-muted)] italic">Protegido</span>
                    )}
                  </div>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      {/* Observation Detail View Modal */}
      <Dialog open={!!selectedObservation} onOpenChange={(open) => !open && setSelectedObservation(null)}>
        {selectedObservation && (
          <>
            <DialogHeader>
              <div className="flex items-start justify-between gap-3 w-full pr-6">
                <div className="space-y-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <Badge variant={selectedObservation.type === "decision" ? "default" : selectedObservation.type === "bugfix" ? "destructive" : "secondary"} className="text-xs">
                      {selectedObservation.type}
                    </Badge>
                    <Badge variant="purple" className="text-xs font-mono">
                      {selectedObservation.project}
                    </Badge>
                    {selectedObservation.scope && (
                      <span className="text-[10px] px-2 py-0.5 rounded bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-[var(--text-muted)]">
                        Alcance: {selectedObservation.scope}
                      </span>
                    )}
                  </div>
                  <DialogTitle className="text-base font-bold text-[var(--text-primary)] leading-snug pt-1">
                    {selectedObservation.title}
                  </DialogTitle>
                </div>
              </div>
              <DialogClose onClick={() => setSelectedObservation(null)} />
            </DialogHeader>

            <div className="space-y-4 mt-4 text-xs">
              {/* Metadata Grid */}
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2.5 p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
                <div>
                  <span className="text-[10px] text-[var(--text-muted)] block uppercase font-mono">ID Local / Ref</span>
                  <span className="font-mono text-xs text-[var(--text-primary)] font-semibold truncate block">
                    #{selectedObservation.id}
                  </span>
                </div>
                <div>
                  <span className="text-[10px] text-[var(--text-muted)] block uppercase font-mono">Confianza</span>
                  <span className="text-xs text-emerald-400 font-semibold">
                    {Math.round((selectedObservation.confidence ?? 1.0) * 100)}%
                  </span>
                </div>
                {selectedObservation.topic_key ? (
                  <div className="col-span-2">
                    <span className="text-[10px] text-[var(--text-muted)] block uppercase font-mono">Topic Key</span>
                    <span className="font-mono text-[11px] text-indigo-400 truncate block">
                      {selectedObservation.topic_key}
                    </span>
                  </div>
                ) : (
                  <div className="col-span-2">
                    <span className="text-[10px] text-[var(--text-muted)] block uppercase font-mono">Fuente</span>
                    <span className="text-xs text-[var(--text-secondary)] truncate block">
                      {selectedObservation.source || "auto / agente"}
                    </span>
                  </div>
                )}
              </div>

              {/* RAG & Semantic Pipeline Panel */}
              <div className="p-3.5 rounded-lg bg-gradient-to-r from-blue-950/30 via-purple-950/20 to-[var(--bg-surface)] border border-blue-500/30 space-y-2.5">
                <div className="flex items-center justify-between flex-wrap gap-2">
                  <div className="flex items-center gap-2">
                    <Sparkles className="h-4 w-4 text-blue-400" />
                    <span className="font-semibold text-xs text-[var(--text-primary)]">Pipeline RAG & Indexación Semántica</span>
                  </div>
                  {selectedObservation.rag_status === "pending" ? (
                    <Badge variant="outline" className="text-[10px] bg-amber-500/10 text-amber-400 border-amber-500/30 flex items-center gap-1 font-mono">
                      <Clock className="h-3 w-3 animate-spin" /> En Cola de Vectorización (Outbox)
                    </Badge>
                  ) : selectedObservation.rag_status === "failed" ? (
                    <Badge variant="outline" className="text-[10px] bg-rose-500/10 text-rose-400 border-rose-500/30 flex items-center gap-1 font-mono">
                      <AlertCircle className="h-3 w-3" /> Error de Vectorización
                    </Badge>
                  ) : (
                    <Badge variant="outline" className="text-[10px] bg-emerald-500/10 text-emerald-400 border-emerald-500/30 flex items-center gap-1 font-mono">
                      <CheckCircle2 className="h-3 w-3" /> Vectorizado y Activo en RAG
                    </Badge>
                  )}
                </div>
                <div className="grid grid-cols-2 sm:grid-cols-3 gap-2.5 text-[11px] pt-1.5 border-t border-[var(--border-subtle)]/70">
                  <div>
                    <span className="text-[10px] text-[var(--text-muted)] block font-mono uppercase">Modelo de Embeddings</span>
                    <span className="font-mono text-[var(--text-primary)] font-medium">
                      {selectedObservation.embedding_model || "text-embedding-3-small"}
                    </span>
                  </div>
                  <div>
                    <span className="text-[10px] text-[var(--text-muted)] block font-mono uppercase">Dimensiones Vectoriales</span>
                    <span className="font-mono text-[var(--text-primary)] font-medium">
                      {selectedObservation.embedding_dimensions ? `${selectedObservation.embedding_dimensions}d` : "1536d"}
                    </span>
                  </div>
                  <div>
                    <span className="text-[10px] text-[var(--text-muted)] block font-mono uppercase">Recuperación Híbrida</span>
                    <span className="text-emerald-400 font-medium font-mono">FTS5 + Cosine (RRF k=60)</span>
                  </div>
                </div>
              </div>

              {/* Full Content Block */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <label className="text-[11px] font-semibold text-[var(--text-secondary)] uppercase tracking-wider flex items-center gap-1.5">
                    <FileText className="h-3.5 w-3.5 text-blue-400" />
                    Contenido Completo & Contexto
                  </label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      navigator.clipboard.writeText(selectedObservation.content);
                      setCopiedContent(true);
                      setTimeout(() => setCopiedContent(false), 2000);
                    }}
                    className="h-7 text-xs gap-1.5 text-blue-400 hover:text-blue-300"
                  >
                    {copiedContent ? (
                      <>
                        <Check className="h-3.5 w-3.5 text-emerald-400" />
                        <span className="text-emerald-400">¡Copiado!</span>
                      </>
                    ) : (
                      <>
                        <Copy className="h-3.5 w-3.5" />
                        <span>Copiar Texto</span>
                      </>
                    )}
                  </Button>
                </div>
                <div className="p-3.5 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] max-h-72 overflow-y-auto font-mono text-xs leading-relaxed whitespace-pre-wrap select-text text-[var(--text-primary)]">
                  {selectedObservation.content}
                </div>
              </div>

              {/* Tags */}
              {selectedObservation.tags && selectedObservation.tags.length > 0 && (
                <div className="space-y-1.5">
                  <label className="text-[11px] font-semibold text-[var(--text-secondary)] uppercase tracking-wider block">
                    Etiquetas / Tags ({selectedObservation.tags.length})
                  </label>
                  <div className="flex flex-wrap gap-1.5">
                    {selectedObservation.tags.map((tag, idx) => (
                      <span key={idx} className="text-[11px] px-2.5 py-1 rounded-md bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-[var(--text-secondary)] font-mono">
                        #{tag}
                      </span>
                    ))}
                  </div>
                </div>
              )}

              {/* Owner / Multi-Tenant Info */}
              {selectedObservation.owner_subject && (
                <div className="text-[10px] text-[var(--text-muted)] flex items-center gap-1.5 pt-1">
                  <Shield className="h-3 w-3 text-purple-400" />
                  <span>Propietario / Subject: <code className="font-mono text-purple-300">{selectedObservation.owner_subject}</code></span>
                </div>
              )}

              {/* Modal Footer */}
              <div className="flex flex-wrap justify-between items-center gap-2 pt-3 border-t border-[var(--border-subtle)]">
                <div>
                  {canDeleteObservation(selectedObservation) && (
                    <Button
                      type="button"
                      variant="destructive"
                      size="sm"
                      onClick={() => {
                        const obs = selectedObservation;
                        setSelectedObservation(null);
                        handleDeleteObservation(obs);
                      }}
                      className="gap-1.5"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      <span>Eliminar Observación</span>
                    </Button>
                  )}
                </div>
                <Button type="button" variant="outline" size="sm" onClick={() => setSelectedObservation(null)}>
                  Cerrar
                </Button>
              </div>
            </div>
          </>
        )}
      </Dialog>

      {/* Create Observation Modal */}
      <Dialog open={isModalOpen} onOpenChange={setIsModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <BrainCircuit className="h-4 w-4 text-blue-400" />
            Registrar Nueva Observación
          </DialogTitle>
          <DialogClose onClick={() => setIsModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleCreateObservation} className="space-y-3.5 mt-4 text-xs">
          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              TÍTULO / RESUMEN
            </label>
            <Input
              type="text"
              value={newTitle}
              onChange={(e) => setNewTitle(e.target.value)}
              placeholder="Ej: Migración de arquitectura o regla de persistencia"
              required
            />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              CONTENIDO / CONTEXTO DETALLADO
            </label>
            <textarea
              className="flex min-h-[90px] w-full rounded-lg border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 py-2 text-xs text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500"
              rows={4}
              value={newContent}
              onChange={(e) => setNewContent(e.target.value)}
              placeholder="Describe la decisión, causa raíz de un bug, o regla..."
              required
            />
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                TIPO
              </label>
              <Select
                value={newType}
                onChange={(e) => setNewType(e.target.value)}
              >
                <option value="decision">Decisión</option>
                <option value="bugfix">Bugfix</option>
                <option value="pattern">Patrón / Convención</option>
                <option value="architecture">Arquitectura</option>
                <option value="discovery">Descubrimiento</option>
                <option value="manual">Manual / Nota</option>
              </Select>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                PROYECTO
              </label>
              <Input
                type="text"
                value={newProject}
                onChange={(e) => setNewProject(e.target.value)}
                required
              />
            </div>
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              TAGS (SEPARADOS POR COMA)
            </label>
            <Input
              type="text"
              value={newTags}
              onChange={(e) => setNewTags(e.target.value)}
              placeholder="db, postgres, schema, performance"
            />
          </div>

          <div className="flex flex-wrap justify-end gap-2 pt-2 border-t border-[var(--border-subtle)]">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" disabled={isCreating}>
              {isCreating ? "Guardando..." : "Guardar Observación"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}
