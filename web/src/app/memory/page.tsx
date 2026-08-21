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
} from "lucide-react";

export default function MemoryPage() {
  const { client } = useAuth();
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

  const { principal } = useAuth();
  const userRoles = principal?.roles || ["admin"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );
  const currentSubject = principal?.id || "";

  const canDeleteObservation = (obs: Observation) => {
    if (isAdmin) return true;
    return true;
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
      <Card className="p-4 bg-slate-900/70 border-slate-800">
        <div className="flex flex-wrap items-center gap-3">
          <div className="flex-1 min-w-[220px]">
            <Input
              type="text"
              placeholder="Filtrar por texto o título..."
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-9 text-xs"
            />
          </div>

          <div className="w-44">
            <Select
              value={filterType}
              onChange={(e) => setFilterType(e.target.value)}
              className="h-9 text-xs"
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

          <div className="w-44">
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
        <Card className="p-12 text-center text-xs text-slate-500 bg-slate-900/50 border-slate-800">
          Cargando observaciones...
        </Card>
      ) : filteredObservations.length === 0 ? (
        <Card className="p-12 text-center bg-slate-900/50 border-slate-800 space-y-3">
          <p className="text-xs text-slate-400">
            No se encontraron observaciones con los filtros actuales.
          </p>
          <Button onClick={() => setIsModalOpen(true)} variant="secondary" size="sm">
            Crear la primera observación
          </Button>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {filteredObservations.map((obs) => (
            <Card key={obs.id} className="p-4 bg-slate-900/80 border-slate-800 flex flex-col justify-between hover:border-slate-700 transition-all">
              <div>
                <div className="flex items-start justify-between gap-2 mb-2">
                  <h3 className="text-xs font-semibold text-slate-100 leading-snug line-clamp-1">
                    {obs.title}
                  </h3>
                  <Badge variant={obs.type === "decision" ? "default" : obs.type === "bugfix" ? "destructive" : "secondary"}>
                    {obs.type}
                  </Badge>
                </div>

                <p className="text-xs text-slate-300 line-clamp-4 leading-relaxed mb-3 whitespace-pre-wrap">
                  {obs.content}
                </p>
              </div>

              <div>
                {obs.tags && obs.tags.length > 0 && (
                  <div className="flex flex-wrap gap-1.5 mb-3">
                    {obs.tags.map((tag, idx) => (
                      <span key={idx} className="text-[10px] px-2 py-0.5 rounded-md bg-slate-950/80 border border-slate-800 text-slate-400 font-mono">
                        #{tag}
                      </span>
                    ))}
                  </div>
                )}

                <div className="flex items-center justify-between pt-3 border-t border-slate-800/80 text-[11px] text-slate-500">
                  <div>
                    <span>Proyecto: <b className="text-slate-300">{obs.project}</b></span>
                  </div>
                  {canDeleteObservation(obs) ? (
                    <Button
                      onClick={() => handleDeleteObservation(obs)}
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-slate-500 hover:text-red-400 hover:bg-red-500/10"
                      title="Eliminar observación (propietario / admin)"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  ) : (
                    <span className="text-[10px] text-[var(--text-muted)] italic">Protegido</span>
                  )}
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

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
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
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
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              CONTENIDO / CONTEXTO DETALLADO
            </label>
            <textarea
              className="flex min-h-[90px] w-full rounded-lg border border-slate-700 bg-slate-950/80 px-3 py-2 text-xs text-slate-100 placeholder:text-slate-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500"
              rows={4}
              value={newContent}
              onChange={(e) => setNewContent(e.target.value)}
              placeholder="Describe la decisión, causa raíz de un bug, o regla..."
              required
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
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
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
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
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              TAGS (SEPARADOS POR COMA)
            </label>
            <Input
              type="text"
              value={newTags}
              onChange={(e) => setNewTags(e.target.value)}
              placeholder="db, postgres, schema, performance"
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
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
