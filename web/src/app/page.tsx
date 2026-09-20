"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth-context";
import { Observation, Session, ServerStats } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardTitle } from "@/components/ui/card";
import { StatCard } from "@/components/shared/StatCard";
import { StatusBadge } from "@/components/shared/StatusBadge";
import { EmptyState } from "@/components/shared/EmptyState";
import { PageHeader } from "@/components/shared/PageHeader";
import {
  BrainCircuit,
  Share2,
  Layers,
  FolderGit2,
  Sparkles,
  Search,
  Clock,
  ArrowRight,
  Shield,
  Plus,
} from "lucide-react";

export default function DashboardPage() {
  const { client, stats, principal } = useAuth();
  const [recentObs, setRecentObs] = useState<Observation[]>([]);
  const [recentSessions, setRecentSessions] = useState<Session[]>([]);
  const [dashboardStats, setDashboardStats] = useState<ServerStats | null>(null);
  const [loading, setLoading] = useState(true);

  const [viewMode, setViewMode] = useState<"personal" | "global">("personal");
  const userRoles = principal?.roles || ["developer"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );

  useEffect(() => {
    if (!client) return;
    setLoading(true);
    const obsQuery = !isAdmin || viewMode === "personal" ? "?limit=6&owner=me" : "?limit=6";
    Promise.all([
      client.listObservations(obsQuery).catch(() => []),
      client.sessions().catch(() => []),
      client.stats().catch(() => null),
    ])
      .then(([obs, sess, liveStats]) => {
        setRecentObs(obs || []);
        if (liveStats) {
          setDashboardStats(liveStats);
        }
        const userFiltered = (sess || []).filter(
          (s) => !s.summary?.startsWith("Imported 90 sessions"),
        );
        setRecentSessions((!isAdmin || viewMode === "personal" ? userFiltered : (sess || [])).slice(0, 5));
      })
      .finally(() => {
        setLoading(false);
      });
  }, [client, isAdmin, viewMode]);

  const userDisplayName =
    principal?.display_name ||
    (principal?.email ? principal.email.split("@")[0] : "") ||
    "Desarrollador";

  const currentStats = dashboardStats ?? stats;
  const totalObservations =
    currentStats?.total_observations ??
    currentStats?.observations ??
    recentObs.length;

  return (
    <div className="space-y-6">
      {/* Page Header */}
      <PageHeader
        title={isAdmin ? "Control Room de Memoria" : `Bienvenido, ${userDisplayName}`}
        badge={
          <StatusBadge
            status={isAdmin ? "admin" : "info"}
            label={isAdmin ? "Admin Workspace" : "Developer Workspace"}
          />
        }
        description={
          isAdmin
            ? "Monitor global de conocimiento semántico, sesiones de codificación y grafo relacional."
            : "Tu espacio de trabajo cognitivo: notas de desarrollo, sesiones de IA y grafo de dependencias."
        }
        actions={
          isAdmin ? (
            <div className="flex items-center p-1 rounded-lg bg-secondary border border-border">
              <button
                type="button"
                onClick={() => setViewMode("personal")}
                className={`px-3 py-1.5 rounded-md text-xs font-semibold transition-colors ${
                  viewMode === "personal"
                    ? "bg-primary text-primary-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                Mi Vista Personal
              </button>
              <button
                type="button"
                onClick={() => setViewMode("global")}
                className={`px-3 py-1.5 rounded-md text-xs font-semibold transition-colors ${
                  viewMode === "global"
                    ? "bg-primary text-primary-foreground shadow-sm"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                Vista Global Tenant
              </button>
            </div>
          ) : null
        }
      />

      {/* Metrics Row - High Information Density & Monospace Numbers */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3.5 sm:gap-4">
        <StatCard
          title={isAdmin && viewMode === "global" ? "Total Observaciones (Tenant)" : "Mis Observaciones"}
          value={totalObservations}
          icon={BrainCircuit}
          iconClassName="text-primary"
          subtext="Notas de arquitectura y contexto"
        />

        <StatCard
          title={isAdmin && viewMode === "global" ? "Aristas de Grafo (Tenant)" : "Mis Vínculos de Grafo"}
          value={isAdmin && viewMode === "global" ? (currentStats?.edges ?? "—") : (currentStats?.edges ?? 0)}
          icon={Share2}
          iconClassName="text-emerald-500"
          subtext="Relaciones de conocimiento activas"
        />

        <StatCard
          title={isAdmin && viewMode === "global" ? "Sesiones Totales" : "Mis Sesiones de Agente"}
          value={
            isAdmin && viewMode === "global"
              ? (currentStats?.active_sessions ?? currentStats?.sessions ?? recentSessions.length)
              : recentSessions.length
          }
          icon={Layers}
          iconClassName="text-amber-500"
          subtext="Sesiones de coding agents indexadas"
        />

        <StatCard
          title={isAdmin && viewMode === "global" ? "Proyectos Activos" : "Mis Proyectos Asignados"}
          value={
            isAdmin && viewMode === "global"
              ? (currentStats?.projects ?? 1)
              : (principal?.projects?.length || Array.from(new Set(recentObs.map((o) => o.project).filter(Boolean))).length || 1)
          }
          icon={FolderGit2}
          iconClassName="text-muted-foreground"
          subtext="Alcances y repositorios vinculados"
        />
      </div>

      {/* Quick Action Operational Banner - Enterprise Calm Solid Style */}
      <Card className="p-4 sm:p-5 border-border bg-card shadow-sm">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
          <div className="space-y-1">
            <h2 className="text-sm font-semibold text-foreground flex items-center gap-2">
              <Sparkles className="h-4 w-4 text-primary shrink-0" />
              <span>Extracción Automática de Conocimiento con LLM</span>
            </h2>
            <p className="text-xs text-muted-foreground">
              Procesa transcripciones de sesiones o notas de código para extraer observaciones y relaciones en 1 paso.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <Link href="/extract">
              <Button size="sm" className="gap-2 text-xs">
                <Sparkles className="h-3.5 w-3.5" />
                <span>Abrir Extractor</span>
              </Button>
            </Link>
            <Link href="/search">
              <Button variant="secondary" size="sm" className="gap-2 text-xs">
                <Search className="h-3.5 w-3.5" />
                <span>Búsqueda y Retrieval</span>
              </Button>
            </Link>
          </div>
        </div>
      </Card>

      {/* Two Column Layout: Observations & Sessions */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4 sm:gap-6">
        {/* Recent Observations */}
        <Card className="p-4 sm:p-5 flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-border mb-4">
              <CardTitle className="text-sm text-foreground">
                <BrainCircuit className="h-4 w-4 text-primary" />
                <span>Últimas Observaciones</span>
              </CardTitle>
              <Link href="/memory">
                <Button variant="ghost" size="sm" className="text-xs text-muted-foreground hover:text-foreground gap-1.5 h-7 px-2">
                  <span>Ver todas</span>
                  <ArrowRight className="h-3.5 w-3.5" />
                </Button>
              </Link>
            </div>

            {loading ? (
              <p className="text-xs text-muted-foreground py-6 text-center">Cargando observaciones...</p>
            ) : recentObs.length === 0 ? (
              <EmptyState
                icon={BrainCircuit}
                title="No tienes observaciones registradas aún"
                description="Al capturar notas desde tu agente MCP (Claude Code, Cursor) o usar el extractor LLM, se indexarán aquí bajo tu perfil."
                action={
                  <Link href="/extract">
                    <Button size="sm" variant="outline" className="text-xs gap-1.5">
                      <Plus className="h-3.5 w-3.5" />
                      <span>Crear o Extraer</span>
                    </Button>
                  </Link>
                }
              />
            ) : (
              <div className="space-y-2.5">
                {recentObs.map((obs) => (
                  <div
                    key={obs.id}
                    className="p-3 bg-secondary/40 border border-border rounded-lg space-y-1.5 hover:border-primary/40 transition-colors"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="font-semibold text-xs text-foreground truncate">
                        {obs.title}
                      </span>
                      <StatusBadge status={obs.type} />
                    </div>
                    <p className="text-xs text-muted-foreground line-clamp-2 leading-relaxed">
                      {obs.content}
                    </p>
                    <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground pt-1">
                      <span>Proyecto: <span className="font-mono text-foreground font-medium">{obs.project}</span></span>
                      <span>•</span>
                      <span className="flex items-center gap-1 font-mono">
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
        <div className="space-y-4 sm:space-y-6">
          {/* Active Sessions */}
          <Card className="p-4 sm:p-5">
            <div className="flex items-center justify-between pb-3 border-b border-border mb-4">
              <CardTitle className="text-sm text-foreground">
                <Layers className="h-4 w-4 text-amber-500" />
                <span>Sesiones Recientes</span>
              </CardTitle>
            </div>
            {recentSessions.length === 0 ? (
              <EmptyState
                icon={Layers}
                title="Sin sesiones activas"
                description="Inicia una sesión desde tu Coding Agent vía MCP para sincronizar el contexto en vivo."
              />
            ) : (
              <div className="space-y-2">
                {recentSessions.map((sess) => (
                  <div
                    key={sess.id}
                    className="p-3 bg-secondary/40 border border-border rounded-lg flex items-center justify-between hover:border-border transition-colors"
                  >
                    <div className="overflow-hidden mr-2">
                      <div className="font-semibold text-xs text-foreground truncate">{sess.project}</div>
                      <div className="text-[11px] text-muted-foreground truncate font-mono">
                        {sess.summary || `ID: ${sess.id.slice(0, 12)}...`}
                      </div>
                    </div>
                    <StatusBadge status="active" label="Activa" showDot />
                  </div>
                ))}
              </div>
            )}
          </Card>

          {/* Principal Clearance Card */}
          <Card className="p-4 sm:p-5">
            <div className="flex items-center justify-between pb-3 border-b border-border mb-3">
              <CardTitle className="text-sm text-foreground">
                <Shield className="h-4 w-4 text-emerald-500" />
                <span>Identidad y Autoridad de Acceso</span>
              </CardTitle>
            </div>
            <div className="space-y-2 text-xs text-muted-foreground">
              <div className="flex justify-between py-1 border-b border-border/50">
                <span>Tipo de Principal:</span>
                <span className="font-mono text-foreground">{principal?.type || "service_account"}</span>
              </div>
              <div className="flex justify-between py-1 border-b border-border/50">
                <span>Organización / Tenant:</span>
                <span className="font-mono text-foreground truncate ml-2">{principal?.org_id || "default-tenant"}</span>
              </div>
              <div className="flex justify-between py-1">
                <span>Roles Asignados:</span>
                <span className="font-semibold font-mono text-primary">{principal?.roles?.join(", ") || "admin"}</span>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
