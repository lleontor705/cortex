"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth-context";
import { Observation, Session } from "@/lib/api";
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

  return (
    <div>
      <div style={{ marginBottom: "28px" }}>
        <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "6px" }}>
          Control Room de Memoria
        </h1>
        <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
          Monitor de conocimiento semántico, sesiones de codificación y grafo relacional de agentes
        </p>
      </div>

      {/* Metrics Row */}
      <div className="metric-grid">
        <div className="metric-card">
          <span className="metric-label">Observaciones</span>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
            <span className="metric-value">{stats?.observations ?? recentObs.length}</span>
            <BrainCircuit size={24} color="var(--accent-primary)" />
          </div>
        </div>

        <div className="metric-card">
          <span className="metric-label">Aristas de Grafo</span>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
            <span className="metric-value">{stats?.edges ?? "—"}</span>
            <Share2 size={24} color="#10b981" />
          </div>
        </div>

        <div className="metric-card">
          <span className="metric-label">Sesiones Activas</span>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
            <span className="metric-value">{stats?.active_sessions ?? stats?.sessions ?? recentSessions.length}</span>
            <Layers size={24} color="#f59e0b" />
          </div>
        </div>

        <div className="metric-card">
          <span className="metric-label">Proyectos</span>
          <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
            <span className="metric-value">{stats?.projects ?? (principal?.projects?.length || 1)}</span>
            <FolderGit2 size={24} color="#8b5cf6" />
          </div>
        </div>
      </div>

      {/* Quick Action Banner */}
      <div
        className="card"
        style={{
          marginBottom: "28px",
          background: "linear-gradient(135deg, rgba(30, 41, 59, 0.9) 0%, rgba(15, 23, 42, 0.9) 100%)",
          borderColor: "var(--border-default)",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", flexWrap: "wrap", gap: "16px" }}>
          <div>
            <h2 style={{ fontSize: "17px", fontWeight: "600", marginBottom: "4px", display: "flex", alignItems: "center", gap: "8px" }}>
              <Sparkles size={18} color="var(--accent-primary)" />
              Extracción Automática de Conocimiento con LLM
            </h2>
            <p style={{ color: "var(--text-secondary)", fontSize: "13px" }}>
              Pasa transcripciones de sesiones o notas de código para extraer observaciones y relaciones con 1 clic.
            </p>
          </div>
          <div style={{ display: "flex", gap: "10px" }}>
            <Link href="/extract" className="btn btn-primary">
              <Sparkles size={14} />
              <span>Abrir Extractor</span>
            </Link>
            <Link href="/search" className="btn btn-secondary">
              <Search size={14} />
              <span>Retrieval Playground</span>
            </Link>
          </div>
        </div>
      </div>

      {/* Two Column Layout */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(400px, 1fr))", gap: "24px" }}>
        {/* Recent Observations */}
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <BrainCircuit size={18} />
              Últimas Observaciones
            </h2>
            <Link href="/memory" className="btn btn-secondary btn-sm">
              <span>Ver todas</span>
              <ArrowRight size={12} />
            </Link>
          </div>

          {loading ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>Cargando observaciones...</p>
          ) : recentObs.length === 0 ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>No hay observaciones guardadas aún.</p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: "12px" }}>
              {recentObs.map((obs) => (
                <div
                  key={obs.id}
                  style={{
                    padding: "12px 14px",
                    backgroundColor: "var(--bg-input)",
                    border: "1px solid var(--border-subtle)",
                    borderRadius: "var(--radius-md)",
                  }}
                >
                  <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", marginBottom: "4px" }}>
                    <span style={{ fontWeight: "600", fontSize: "13px", color: "var(--text-primary)" }}>
                      {obs.title}
                    </span>
                    <span className={`badge ${obs.type === "decision" ? "badge-blue" : obs.type === "bugfix" ? "badge-amber" : "badge-zinc"}`}>
                      {obs.type}
                    </span>
                  </div>
                  <p style={{ color: "var(--text-secondary)", fontSize: "12px", marginBottom: "8px", overflow: "hidden", textOverflow: "ellipsis", display: "-webkit-box", WebkitLineClamp: 2, WebkitBoxOrient: "vertical" }}>
                    {obs.content}
                  </p>
                  <div style={{ display: "flex", alignItems: "center", gap: "12px", fontSize: "11px", color: "var(--text-muted)" }}>
                    <span>Proyecto: <b>{obs.project}</b></span>
                    <span>•</span>
                    <span style={{ display: "flex", alignItems: "center", gap: "4px" }}>
                      <Clock size={11} />
                      {new Date(obs.created_at).toLocaleDateString()}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Sessions & Workspace Authority */}
        <div style={{ display: "flex", flexDirection: "column", gap: "24px" }}>
          {/* Active Sessions */}
          <div className="card">
            <div className="card-header">
              <h2 className="card-title">
                <Layers size={18} />
                Sesiones Recientes
              </h2>
            </div>
            {recentSessions.length === 0 ? (
              <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>No hay sesiones activas registradas.</p>
            ) : (
              <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
                {recentSessions.map((sess) => (
                  <div
                    key={sess.id}
                    style={{
                      padding: "10px 14px",
                      backgroundColor: "var(--bg-input)",
                      border: "1px solid var(--border-subtle)",
                      borderRadius: "var(--radius-md)",
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "space-between",
                    }}
                  >
                    <div>
                      <div style={{ fontWeight: "600", fontSize: "13px" }}>{sess.project}</div>
                      <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>
                        {sess.summary || `ID: ${sess.id.slice(0, 12)}...`}
                      </div>
                    </div>
                    <span className="badge badge-green">Activa</span>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Principal Clearance Card */}
          <div className="card">
            <div className="card-header">
              <h2 className="card-title">
                <Shield size={18} />
                Identidad y Autoridad de Acceso
              </h2>
            </div>
            <div style={{ fontSize: "13px", color: "var(--text-secondary)", display: "flex", flexDirection: "column", gap: "8px" }}>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span>Tipo de Principal:</span>
                <span className="font-mono" style={{ color: "var(--text-primary)" }}>{principal?.type || "service_account"}</span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span>Organización / Tenant:</span>
                <span className="font-mono" style={{ color: "var(--text-primary)" }}>{principal?.org_id || "default-tenant"}</span>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span>Roles Asignados:</span>
                <span style={{ color: "var(--text-primary)" }}>{principal?.roles?.join(", ") || "admin"}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
