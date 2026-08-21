"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth-context";
import { Observation, Session } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import {
  BrainCircuit,
  Share2,
  Layers,
  FolderGit2,
  Sparkles,
  Search,
  Plus,
  Clock,
  ArrowRight,
  Shield,
} from "lucide-react";

export default function DashboardPage() {
  const { client, stats, principal } = useAuth();
  const [recentObs, setRecentObs] = useState<Observation[]>([]);
  const [recentSessions, setRecentSessions] = useState<Session[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!client) return;
    setLoading(true);
    Promise.all([
      client.listObservations("?limit=6").catch(() => []),
      client.sessions().catch(() => []),
    ])
      .then(([obs, sess]) => {
        setRecentObs(obs || []);
        setRecentSessions((sess || []).slice(0, 5));
      })
      .finally(() => {
        setLoading(false);
      });
  }, [client]);

  const [viewMode, setViewMode] = useState<"personal" | "global">("personal");
  const userRoles = principal?.roles || ["admin"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-[var(--text-primary)]">
            Control Room de Memoria
          </h1>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Monitor de conocimiento semántico, sesiones de codificación y grafo relacional de agentes
          </p>
        </div>

        {/* View Mode Selector (Admin vs User) */}
        {isAdmin && (
          <div className="flex items-center p-1 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)]">
            <button
              type="button"
              onClick={() => setViewMode("personal")}
              className={`px-3 py-1.5 rounded-md text-xs font-semibold transition-all ${
                viewMode === "personal"
                  ? "bg-[var(--accent-primary)] text-white shadow-sm"
                  : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              }`}
            >
              👤 Mi Vista Personal
            </button>
            <button
              type="button"
              onClick={() => setViewMode("global")}
              className={`px-3 py-1.5 rounded-md text-xs font-semibold transition-all ${
                viewMode === "global"
                  ? "bg-[var(--accent-primary)] text-white shadow-sm"
                  : "text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              }`}
            >
              🏢 Vista Global Tenant (Admin)
            </button>
          </div>
        )}
      </div>

      {/* Metrics Row */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-sm">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
            {viewMode === "global" ? "Total Observaciones (Tenant)" : "Mis Observaciones"}
          </span>
          <div className="flex items-center justify-between mt-2">
            <span className="text-2xl font-bold text-[var(--text-primary)]">
              {stats?.observations ?? recentObs.length}
            </span>
            <BrainCircuit className="h-6 w-6 text-blue-500" />
          </div>
        </Card>

        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-sm">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
            {viewMode === "global" ? "Aristas de Grafo (Tenant)" : "Mis Vínculos de Grafo"}
          </span>
          <div className="flex items-center justify-between mt-2">
            <span className="text-2xl font-bold text-emerald-500">{stats?.edges ?? "—"}</span>
            <Share2 className="h-6 w-6 text-emerald-500" />
          </div>
        </Card>

        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-sm">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
            {viewMode === "global" ? "Sesiones Totales" : "Mis Sesiones de Agente"}
          </span>
          <div className="flex items-center justify-between mt-2">
            <span className="text-2xl font-bold text-amber-500">
              {stats?.active_sessions ?? stats?.sessions ?? recentSessions.length}
            </span>
            <Layers className="h-6 w-6 text-amber-500" />
          </div>
        </Card>

        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-sm">
          <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
            {viewMode === "global" ? "Proyectos Activos" : "Mis Proyectos Asignados"}
          </span>
          <div className="flex items-center justify-between mt-2">
            <span className="text-2xl font-bold text-purple-500">
              {stats?.projects ?? (principal?.projects?.length || 1)}
            </span>
            <FolderGit2 className="h-6 w-6 text-purple-500" />
          </div>
        </Card>
      </div>

      {/* Quick Action Banner */}
      <Card className="p-6 bg-gradient-to-r from-blue-900/20 via-[var(--bg-surface)] to-purple-900/20 border-[var(--border-subtle)] shadow-md">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="space-y-1">
            <h2 className="text-base font-semibold text-[var(--text-primary)] flex items-center gap-2">
              <Sparkles className="h-5 w-5 text-blue-400" />
              Extracción Automática de Conocimiento con LLM
            </h2>
            <p className="text-xs text-[var(--text-secondary)]">
              Pasa transcripciones de sesiones o notas de código para extraer observaciones y relaciones con 1 clic.
            </p>
          </div>
          <div className="flex items-center gap-2.5">
            <Link href="/extract">
              <Button size="sm" className="gap-2">
                <Sparkles className="h-4 w-4" />
                <span>Abrir Extractor</span>
              </Button>
            </Link>
            <Link href="/search">
              <Button variant="secondary" size="sm" className="gap-2">
                <Search className="h-4 w-4" />
                <span>Retrieval Playground</span>
              </Button>
            </Link>
          </div>
        </div>
      </Card>

      {/* Two Column Layout */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Recent Observations */}
        <Card className="p-5 bg-slate-900/70 border-slate-800 flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
              <CardTitle className="text-sm">
                <BrainCircuit className="h-4 w-4 text-blue-400" />
                Últimas Observaciones
              </CardTitle>
              <Link href="/memory">
                <Button variant="ghost" size="sm" className="text-xs text-slate-400 hover:text-white gap-1.5 h-7">
                  <span>Ver todas</span>
                  <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </Link>
            </div>

            {loading ? (
              <p className="text-xs text-slate-500 py-6 text-center">Cargando observaciones...</p>
            ) : recentObs.length === 0 ? (
              <p className="text-xs text-slate-500 py-6 text-center">No hay observaciones guardadas aún.</p>
            ) : (
              <div className="space-y-3">
                {recentObs.map((obs) => (
                  <div
                    key={obs.id}
                    className="p-3.5 bg-slate-950/70 border border-slate-800/80 rounded-lg space-y-1.5 hover:border-slate-700 transition-colors"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-semibold text-xs text-slate-200 truncate">
                        {obs.title}
                      </span>
                      <Badge variant={obs.type === "decision" ? "default" : obs.type === "bugfix" ? "warning" : "secondary"}>
                        {obs.type}
                      </Badge>
                    </div>
                    <p className="text-xs text-slate-400 line-clamp-2 leading-relaxed">
                      {obs.content}
                    </p>
                    <div className="flex items-center gap-2 text-[11px] text-slate-500 pt-1">
                      <span>Proyecto: <b className="text-slate-400">{obs.project}</b></span>
                      <span>•</span>
                      <span className="flex items-center gap-1">
                        <Clock className="h-3 w-3" />
                        {new Date(obs.created_at).toLocaleDateString()}
                      </span>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </Card>

        {/* Sessions & Access Authority */}
        <div className="space-y-6">
          {/* Active Sessions */}
          <Card className="p-5 bg-slate-900/70 border-slate-800">
            <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
              <CardTitle className="text-sm">
                <Layers className="h-4 w-4 text-amber-400" />
                Sesiones Recientes
              </CardTitle>
            </div>
            {recentSessions.length === 0 ? (
              <p className="text-xs text-slate-500 py-4 text-center">No hay sesiones activas registradas.</p>
            ) : (
              <div className="space-y-2.5">
                {recentSessions.map((sess) => (
                  <div
                    key={sess.id}
                    className="p-3 bg-slate-950/70 border border-slate-800/80 rounded-lg flex items-center justify-between hover:border-slate-700 transition-colors"
                  >
                    <div>
                      <div className="font-semibold text-xs text-slate-200">{sess.project}</div>
                      <div className="text-[11px] text-slate-500">
                        {sess.summary || `ID: ${sess.id.slice(0, 12)}...`}
                      </div>
                    </div>
                    <Badge variant="success">Activa</Badge>
                  </div>
                ))}
              </div>
            )}
          </Card>

          {/* Principal Clearance Card */}
          <Card className="p-5 bg-slate-900/70 border-slate-800">
            <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-3">
              <CardTitle className="text-sm">
                <Shield className="h-4 w-4 text-emerald-400" />
                Identidad y Autoridad de Acceso
              </CardTitle>
            </div>
            <div className="space-y-2.5 text-xs text-slate-400">
              <div className="flex justify-between py-1 border-b border-slate-850">
                <span>Tipo de Principal:</span>
                <span className="font-mono text-slate-200">{principal?.type || "service_account"}</span>
              </div>
              <div className="flex justify-between py-1 border-b border-slate-850">
                <span>Organización / Tenant:</span>
                <span className="font-mono text-slate-200">{principal?.org_id || "default-tenant"}</span>
              </div>
              <div className="flex justify-between py-1">
                <span>Roles Asignados:</span>
                <span className="font-semibold text-blue-400">{principal?.roles?.join(", ") || "admin"}</span>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
