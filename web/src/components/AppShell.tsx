"use client";

import React, { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  LayoutDashboard,
  BrainCircuit,
  Share2,
  Search,
  Sparkles,
  ShieldCheck,
  Settings,
  Server,
  Key,
  LogOut,
  RefreshCw,
  CheckCircle2,
  AlertCircle,
} from "lucide-react";

export default function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const {
    serverUrl,
    token,
    principal,
    isConnected,
    isLoading,
    error,
    setCredentials,
    refreshState,
    logout,
  } = useAuth();

  const [inputUrl, setInputUrl] = useState(serverUrl);
  const [inputToken, setInputToken] = useState(token || "");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);

  const handleConnect = async (e: React.FormEvent) => {
    e.preventDefault();
    setIsSubmitting(true);
    setConnectError(null);
    const success = await setCredentials(inputUrl, inputToken);
    if (!success) {
      setConnectError("No se pudo autenticar. Verifique la URL y el Bearer Token.");
    }
    setIsSubmitting(false);
  };

  const navItems = [
    { href: "/", label: "Dashboard", icon: LayoutDashboard },
    { href: "/memory", label: "Memoria & Notas", icon: BrainCircuit },
    { href: "/graph", label: "Grafo de Conocimiento", icon: Share2 },
    { href: "/search", label: "Retrieval Playground", icon: Search },
    { href: "/extract", label: "Extracción LLM", icon: Sparkles },
    { href: "/admin", label: "Agentes & Usuarios", icon: ShieldCheck },
    { href: "/settings", label: "Configuración", icon: Settings },
  ];

  if (!isConnected && !isLoading) {
    return (
      <div style={{
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: "var(--bg-primary)",
        padding: "20px",
      }}>
        <div className="card" style={{ maxWidth: "480px", width: "100%", padding: "36px" }}>
          <div style={{ textAlign: "center", marginBottom: "28px" }}>
            <div style={{
              width: "56px",
              height: "56px",
              borderRadius: "16px",
              backgroundColor: "rgba(59, 130, 246, 0.15)",
              color: "var(--accent-primary)",
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              margin: "0 auto 16px",
            }}>
              <BrainCircuit size={32} />
            </div>
            <h1 style={{ fontSize: "22px", fontWeight: "700", marginBottom: "6px" }}>Cortex Control Room</h1>
            <p style={{ color: "var(--text-secondary)", fontSize: "13px" }}>
              Memoria persistente y grafo de conocimiento para coding agents
            </p>
          </div>

          {(connectError || error) && (
            <div style={{
              backgroundColor: "var(--danger-bg)",
              border: "1px solid rgba(239, 68, 68, 0.3)",
              color: "var(--danger)",
              padding: "12px 16px",
              borderRadius: "var(--radius-md)",
              fontSize: "13px",
              marginBottom: "20px",
              display: "flex",
              alignItems: "center",
              gap: "8px",
            }}>
              <AlertCircle size={16} />
              <span>{connectError || error}</span>
            </div>
          )}

          <form onSubmit={handleConnect} style={{ display: "flex", flexDirection: "column", gap: "16px" }}>
            <div>
              <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "6px" }}>
                CORTEX SERVER URL
              </label>
              <div style={{ position: "relative" }}>
                <input
                  type="text"
                  className="input"
                  value={inputUrl}
                  onChange={(e) => setInputUrl(e.target.value)}
                  placeholder="http://localhost:7438"
                  required
                />
              </div>
            </div>

            <div>
              <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "6px" }}>
                BEARER TOKEN / AUTH KEY
              </label>
              <input
                type="password"
                className="input"
                value={inputToken}
                onChange={(e) => setInputToken(e.target.value)}
                placeholder="cortex_sec_..."
                required
              />
            </div>

            <button
              type="submit"
              className="btn btn-primary"
              disabled={isSubmitting}
              style={{ width: "100%", marginTop: "10px", padding: "12px" }}
            >
              {isSubmitting ? "Conectando..." : "Conectar al Servidor"}
            </button>
          </form>

          <div style={{ marginTop: "24px", paddingTop: "20px", borderTop: "1px solid var(--border-subtle)", textAlign: "center" }}>
            <p style={{ fontSize: "12px", color: "var(--text-muted)" }}>
              Para desarrollo local usa el token configurado en <code className="font-mono">docker-compose.yml</code> o <code className="font-mono">CORTEX_HTTP_TOKEN</code>.
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="app-container">
      {/* Sidebar */}
      <aside className="sidebar">
        <div style={{ padding: "20px 24px", borderBottom: "1px solid var(--border-subtle)", display: "flex", alignItems: "center", gap: "10px" }}>
          <div style={{
            width: "34px",
            height: "34px",
            borderRadius: "10px",
            backgroundColor: "rgba(59, 130, 246, 0.2)",
            color: "var(--accent-primary)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}>
            <BrainCircuit size={20} />
          </div>
          <div>
            <div style={{ fontWeight: "700", fontSize: "15px", letterSpacing: "-0.02em" }}>CORTEX</div>
            <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>Agent Memory v2.0</div>
          </div>
        </div>

        <nav style={{ flex: 1, padding: "16px 0", overflowY: "auto" }}>
          {navItems.map((item) => {
            const Icon = item.icon;
            const isActive = pathname === item.href;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`nav-link ${isActive ? "active" : ""}`}
              >
                <Icon size={18} />
                <span>{item.label}</span>
              </Link>
            );
          })}
        </nav>

        {/* Sidebar Footer */}
        <div style={{ padding: "16px", borderTop: "1px solid var(--border-subtle)" }}>
          <div style={{
            backgroundColor: "rgba(15, 23, 42, 0.6)",
            border: "1px solid var(--border-subtle)",
            borderRadius: "var(--radius-md)",
            padding: "12px",
            marginBottom: "12px",
          }}>
            <div style={{ display: "flex", alignItems: "center", gap: "6px", marginBottom: "4px" }}>
              <CheckCircle2 size={13} color="var(--success)" />
              <span style={{ fontSize: "11px", fontWeight: "600", color: "var(--success)" }}>Conectado</span>
            </div>
            <div style={{ fontSize: "11px", color: "var(--text-muted)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
              {principal ? (principal.id || "Service Principal") : "Local SQLite Mode"}
            </div>
          </div>

          <button
            onClick={logout}
            className="btn btn-secondary btn-sm"
            style={{ width: "100%", justifyContent: "center" }}
          >
            <LogOut size={14} />
            <span>Desconectar</span>
          </button>
        </div>
      </aside>

      {/* Main Content Area */}
      <main className="main-content">
        <header className="top-bar">
          <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
            <span className="badge badge-zinc">
              <Server size={11} />
              {serverUrl}
            </span>
            {principal?.workspaces?.length ? (
              <span className="badge badge-blue">
                WS: {principal.workspaces.join(", ")}
              </span>
            ) : null}
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <button
              onClick={() => refreshState()}
              className="btn btn-secondary btn-sm"
              title="Refrescar estado"
            >
              <RefreshCw size={14} />
              <span>Refrescar</span>
            </button>
          </div>
        </header>

        <div className="content-body">{children}</div>
      </main>
    </div>
  );
}
