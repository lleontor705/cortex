"use client";

import React, { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  initialSecretInput,
  observeResetGeneration,
  type SecretInputState,
} from "@/lib/form-secret-reset";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  LayoutDashboard,
  BrainCircuit,
  Share2,
  Search,
  Sparkles,
  ShieldCheck,
  FolderKanban,
  Settings,
  Server,
  Key,
  LogOut,
  RefreshCw,
  CheckCircle2,
  AlertCircle,
  Cpu,
  Terminal,
  Activity,
  Sun,
  Moon,
  Cloud,
  CloudOff,
  User,
} from "lucide-react";

export default function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const {
    serverUrl,
    token,
    resetGeneration,
    principal,
    isConnected,
    isLoading,
    error,
    setCredentials,
    refreshState,
    logout,
  } = useAuth();

  const [inputUrl, setInputUrl] = useState(serverUrl);
  const [secretInput, setSecretInput] = useState<SecretInputState>(() =>
    initialSecretInput(token || "", resetGeneration),
  );
  const inputToken = secretInput.typed;
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [connectError, setConnectError] = useState<string | null>(null);
  const [isLightMode, setIsLightMode] = useState<boolean>(false);
  const [cloudSyncEnabled, setCloudSyncEnabled] = useState<boolean>(true);

  useEffect(() => {
    setSecretInput((state) => observeResetGeneration(state, resetGeneration));
  }, [resetGeneration]);

  useEffect(() => {
    const savedTheme = localStorage.getItem("cortex_theme");
    if (savedTheme === "light") {
      setIsLightMode(true);
      document.documentElement.classList.add("light");
    } else {
      setIsLightMode(false);
      document.documentElement.classList.remove("light");
    }
  }, []);

  const toggleTheme = () => {
    const next = !isLightMode;
    setIsLightMode(next);
    if (next) {
      document.documentElement.classList.add("light");
      localStorage.setItem("cortex_theme", "light");
    } else {
      document.documentElement.classList.remove("light");
      localStorage.setItem("cortex_theme", "dark");
    }
  };

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

  const userRoles = principal?.roles || ["admin"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );
  const userName = principal?.id || "usrLuisLeon";
  const primaryRole = userRoles[0] || (isAdmin ? "admin" : "member");

  const allNavItems = [
    { href: "/", label: "Dashboard", icon: LayoutDashboard, badge: "Live", minRole: "all" },
    { href: "/projects", label: "Proyectos & Skills", icon: FolderKanban, badge: "MCP", minRole: "all" },
    { href: "/memory", label: "Memoria & Notas", icon: BrainCircuit, minRole: "all" },
    { href: "/graph", label: "Grafo de Conocimiento", icon: Share2, badge: "2D Force", minRole: "all" },
    { href: "/search", label: "Retrieval Playground", icon: Search, minRole: "all" },
    { href: "/extract", label: "Extracción LLM", icon: Sparkles, badge: "AI", minRole: "all" },
    { href: "/admin", label: "Agentes & Tokens", icon: ShieldCheck, minRole: "admin" },
    { href: "/settings", label: "Configuración Servidor", icon: Settings, minRole: "admin" },
  ];

  const navItems = allNavItems.filter((item) => {
    if (item.minRole === "admin" && !isAdmin) return false;
    return true;
  });

  if (!isConnected && !isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-950 p-4 relative overflow-hidden">
        {/* Background glow ambient effects */}
        <div className="absolute top-1/4 left-1/2 -translate-x-1/2 -translate-y-1/2 w-96 h-96 bg-blue-600/15 rounded-full blur-3xl pointer-events-none" />
        <div className="absolute bottom-1/4 right-1/3 w-80 h-80 bg-purple-600/10 rounded-full blur-3xl pointer-events-none" />

        <Card className="max-w-md w-full p-8 bg-slate-900/90 border-slate-800 shadow-2xl backdrop-blur-xl relative z-10">
          <div className="text-center mb-6">
            <div className="w-14 h-14 rounded-2xl bg-blue-500/10 border border-blue-500/30 text-blue-400 flex items-center justify-center mx-auto mb-4 shadow-lg shadow-blue-500/10">
              <BrainCircuit className="h-7 w-7" />
            </div>
            <h1 className="text-xl font-bold text-white tracking-tight">CORTEX CONTROL ROOM</h1>
            <p className="text-xs text-slate-400 mt-1">
              Memoria persistente y arquitectura cognitiva para coding agents
            </p>
          </div>

          {(connectError || error) && (
            <div className="bg-red-500/10 border border-red-500/30 text-red-400 p-3 rounded-lg text-xs mb-5 flex items-center gap-2.5">
              <AlertCircle className="h-4 w-4 shrink-0" />
              <span>{connectError || error}</span>
            </div>
          )}

          <form onSubmit={handleConnect} className="space-y-4 text-xs">
            <div className="space-y-1.5">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase tracking-wider">
                CORTEX SERVER ENDPOINT
              </label>
              <Input
                type="text"
                value={inputUrl}
                onChange={(e) => setInputUrl(e.target.value)}
                placeholder="http://localhost:7438"
                required
                className="h-10 text-xs bg-slate-950/80"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase tracking-wider">
                BEARER TOKEN / AUTH KEY
              </label>
              <Input
                type="password"
                value={inputToken}
                onChange={(e) =>
                  setSecretInput((state) => ({ ...state, typed: e.target.value }))
                }
                placeholder="cortex_sec_..."
                required
                className="h-10 text-xs bg-slate-950/80"
              />
            </div>

            <Button
              type="submit"
              disabled={isSubmitting}
              className="w-full h-10 mt-2 text-xs font-semibold shadow-lg shadow-blue-600/20"
            >
              {isSubmitting ? "Conectando..." : "Conectar con Token"}
            </Button>
          </form>

          <div className="mt-6 pt-5 border-t border-slate-800 text-center flex items-center justify-between">
            <p className="text-[11px] text-slate-500 text-left">
              Soporta tokens <code className="text-slate-400 font-mono">admin</code> y <code className="text-slate-400 font-mono">member</code>.
            </p>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={toggleTheme}
              className="text-xs text-slate-400 hover:text-white"
            >
              {isLightMode ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="flex min-h-screen bg-[var(--bg-primary)] text-[var(--text-primary)] antialiased font-sans transition-colors duration-200">
      {/* Sleek Sidebar */}
      <aside className="w-64 bg-[var(--bg-secondary)] border-r border-[var(--border-subtle)] flex flex-col shrink-0 sticky top-0 h-screen backdrop-blur-md z-30">
        {/* Brand Header */}
        <div className="p-5 border-b border-[var(--border-subtle)] flex items-center justify-between">
          <div className="flex items-center gap-3">
            <div className="w-9 h-9 rounded-xl bg-gradient-to-br from-blue-500 to-indigo-600 text-white flex items-center justify-center shadow-lg shadow-blue-500/20">
              <BrainCircuit className="h-5 w-5" />
            </div>
            <div>
              <div className="font-bold text-sm tracking-tight flex items-center gap-1.5">
                <span>CORTEX</span>
                <Badge variant="purple" className="text-[9px] px-1.5 py-0 h-4">v2.0</Badge>
              </div>
              <div className="text-[10px] text-[var(--text-muted)] font-medium">Cognitive Memory Plane</div>
            </div>
          </div>

          {/* Theme Toggle Button */}
          <Button
            onClick={toggleTheme}
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0 text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            title={isLightMode ? "Cambiar a Modo Oscuro" : "Cambiar a Modo Claro"}
          >
            {isLightMode ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4 text-amber-400" />}
          </Button>
        </div>

        {/* User Identity Card */}
        <div className="px-3.5 py-2.5 mx-3 mt-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-center justify-between">
          <div className="flex items-center gap-2 overflow-hidden">
            <div className="w-7 h-7 rounded-full bg-blue-500/20 text-blue-400 flex items-center justify-center shrink-0">
              <User className="h-3.5 w-3.5" />
            </div>
            <div className="overflow-hidden">
              <div className="text-xs font-semibold truncate">{userName}</div>
              <div className="text-[9px] text-[var(--text-muted)] uppercase tracking-wider">
                {primaryRole}
              </div>
            </div>
          </div>
          <Badge
            variant={isAdmin ? "default" : "secondary"}
            className="text-[9px] px-1.5 py-0 h-4 font-mono uppercase"
          >
            {primaryRole}
          </Badge>
        </div>

        {/* Navigation List */}
        <nav className="flex-1 py-3 px-3 space-y-1 overflow-y-auto">
          {navItems.map((item) => {
            const Icon = item.icon;
            const isActive = pathname === item.href;
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`flex items-center justify-between px-3.5 py-2.5 rounded-lg text-xs font-medium transition-all duration-150 ${
                  isActive
                    ? "bg-[var(--accent-primary)] text-white shadow-md shadow-blue-600/20 font-semibold"
                    : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface-hover)]"
                }`}
              >
                <div className="flex items-center gap-2.5">
                  <Icon className={`h-4 w-4 ${isActive ? "text-white" : "text-[var(--text-secondary)]"}`} />
                  <span>{item.label}</span>
                </div>
                {item.badge && (
                  <span
                    className={`text-[9px] px-1.5 py-0.5 rounded-full font-mono font-semibold ${
                      isActive
                        ? "bg-white/20 text-white"
                        : "bg-[var(--bg-surface)] text-[var(--text-muted)] border border-[var(--border-subtle)]"
                    }`}
                  >
                    {item.badge}
                  </span>
                )}
              </Link>
            );
          })}
        </nav>

        {/* Sidebar Footer / System Status */}
        <div className="p-3.5 border-t border-[var(--border-subtle)] bg-[var(--bg-surface)] space-y-3">
          <div className="p-2.5 rounded-lg bg-[var(--bg-secondary)] border border-[var(--border-subtle)] space-y-1.5">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                <span className="text-[11px] font-semibold text-emerald-400">
                  {cloudSyncEnabled ? "Cloud Sync: ON" : "Local Only"}
                </span>
              </div>
              <button
                type="button"
                onClick={() => setCloudSyncEnabled(!cloudSyncEnabled)}
                title="Alternar subida a la nube"
                className="text-[var(--text-muted)] hover:text-blue-400 transition-colors"
              >
                {cloudSyncEnabled ? <Cloud className="h-3.5 w-3.5 text-blue-400" /> : <CloudOff className="h-3.5 w-3.5" />}
              </button>
            </div>
            <div className="text-[10px] text-[var(--text-muted)] truncate font-mono">
              {principal ? principal.id || "Connected Principal" : "Local SQLite Node"}
            </div>
          </div>

          <Button
            onClick={logout}
            variant="ghost"
            size="sm"
            className="w-full justify-center text-xs text-[var(--text-secondary)] hover:text-red-400 hover:bg-red-500/10 h-8"
          >
            <LogOut className="h-3.5 w-3.5 mr-1.5" />
            <span>Cerrar Sesión</span>
          </Button>
        </div>
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0 overflow-y-auto">
        {/* Top Bar */}
        <header className="h-16 bg-[var(--bg-secondary)] backdrop-blur-md border-b border-[var(--border-subtle)] flex items-center justify-between px-7 sticky top-0 z-20">
          <div className="flex items-center gap-3">
            <Badge variant="secondary" className="gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)]">
              <Server className="h-3 w-3 text-blue-400" />
              <span className="font-mono text-[11px]">{serverUrl}</span>
            </Badge>
            {principal?.workspaces?.length ? (
              <Badge variant="default" className="text-[11px]">
                WS: {principal.workspaces.join(", ")}
              </Badge>
            ) : null}
            <Badge
              variant={cloudSyncEnabled ? "default" : "outline"}
              className="text-[10px] cursor-pointer"
              onClick={() => setCloudSyncEnabled(!cloudSyncEnabled)}
            >
              {cloudSyncEnabled ? "☁️ Sync Activo" : "🔒 Local Only"}
            </Badge>
          </div>

          <div className="flex items-center gap-2.5">
            {/* Theme Toggle Button */}
            <Button
              onClick={toggleTheme}
              variant="outline"
              size="sm"
              className="h-8 px-2.5 text-xs gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            >
              {isLightMode ? (
                <>
                  <Moon className="h-3.5 w-3.5" />
                  <span>Oscuro</span>
                </>
              ) : (
                <>
                  <Sun className="h-3.5 w-3.5 text-amber-400" />
                  <span>Claro</span>
                </>
              )}
            </Button>

            <Button
              onClick={() => refreshState()}
              variant="outline"
              size="sm"
              className="h-8 text-xs gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              title="Refrescar estado"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              <span>Refrescar</span>
            </Button>
          </div>
        </header>

        {/* Content Body Container */}
        <main className="p-7 max-w-7xl w-full mx-auto space-y-6">{children}</main>
      </div>
    </div>
  );
}
