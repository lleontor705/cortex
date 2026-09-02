"use client";

import React, { useEffect, useRef, useState } from "react";
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
  Menu,
  X,
  MessageCircleQuestion,
} from "lucide-react";

export default function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const {
    serverUrl,
    managedServerEndpoint,
    token,
    resetGeneration,
    principal,
    workspaceId,
    setWorkspace,
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
  const [mobileMenuOpen, setMobileMenuOpen] = useState<boolean>(false);
  const [latencyMs, setLatencyMs] = useState<number | null>(null);
  const mobileMenuButtonRef = useRef<HTMLButtonElement | null>(null);
  const mobileDrawerRef = useRef<HTMLElement | null>(null);
  const mobileCloseButtonRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    if (!isConnected || !serverUrl) return;

    let isMounted = true;
    const checkLatency = async () => {
      try {
        const start = performance.now();
        const res = await fetch(`${serverUrl.replace(/\/$/, '')}/health`, {
          method: 'GET',
          cache: 'no-store',
        });
        const duration = Math.round(performance.now() - start);
        if (isMounted && res.ok) {
          setLatencyMs(duration);
        }
      } catch {
        if (isMounted) setLatencyMs(null);
      }
    };

    checkLatency();
    const interval = setInterval(checkLatency, 15000);
    return () => {
      isMounted = false;
      clearInterval(interval);
    };
  }, [isConnected, serverUrl]);

  useEffect(() => {
    setMobileMenuOpen(false);
  }, [pathname]);

  useEffect(() => {
    setSecretInput((state) => observeResetGeneration(state, resetGeneration));
  }, [resetGeneration]);

  useEffect(() => {
    if (!mobileMenuOpen) return;
    mobileCloseButtonRef.current?.focus();
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setMobileMenuOpen(false);
        return;
      }
      if (event.key !== "Tab" || !mobileDrawerRef.current) return;
      const focusable = Array.from(
        mobileDrawerRef.current.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])',
        ),
      );
      if (!focusable.length) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      mobileMenuButtonRef.current?.focus();
    };
  }, [mobileMenuOpen]);

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
    const success = await setCredentials(managedServerEndpoint ? serverUrl : inputUrl, inputToken);
    if (!success) {
      setConnectError("No se pudo autenticar. Verifique la URL y el Bearer Token.");
    }
    setIsSubmitting(false);
  };

  const userRoles = principal?.roles || ["developer"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );
  const isDeveloper = userRoles.some(
    (r) =>
      r.toLowerCase() === "developer" ||
      r.toLowerCase() === "member" ||
      r.toLowerCase() === "admin" ||
      r.toLowerCase() === "owner",
  );
  const displayName =
    principal?.display_name ||
    (principal?.email ? principal.email.split("@")[0] : "") ||
    (principal?.id ? `ID: ${principal.id.slice(0, 8)}...` : "Usuario Cortex");
  const userEmail = principal?.email || "";
  const primaryRole = userRoles[0] || (isAdmin ? "admin" : isDeveloper ? "developer" : "member");
  const grantedWorkspaces = principal?.workspaces || [];
  const selectedWorkspace = workspaceId || grantedWorkspaces[0] || "";
  const changeWorkspace = (event: React.ChangeEvent<HTMLSelectElement>) => {
    void setWorkspace(event.target.value);
  };

  const navGroups = [
    {
      label: "Conocimiento",
      items: [
        { href: "/agent", label: "Preguntar", icon: MessageCircleQuestion, badge: "RAG", minRole: "all" },
        { href: "/search", label: "Explorar", icon: Search, minRole: "all" },
        { href: "/memory", label: "Memoria", icon: BrainCircuit, minRole: "all" },
        { href: "/code", label: "Código", icon: Terminal, minRole: "all" },
        { href: "/graph", label: "Grafo", icon: Share2, minRole: "all" },
      ],
    },
    {
      label: "Operaciones",
      items: [
        { href: "/", label: "Inicio", icon: LayoutDashboard, badge: "Live", minRole: "all" },
        { href: "/projects", label: "Proyectos", icon: FolderKanban, badge: "MCP", minRole: "all" },
        { href: "/extract", label: "Extracción", icon: Sparkles, badge: "AI", minRole: "all" },
      ],
    },
    {
      label: "Administración",
      items: [
        { href: "/admin", label: "Agentes y tokens", icon: ShieldCheck, minRole: "admin" },
        { href: "/settings", label: "Servidor", icon: Settings, minRole: "admin" },
      ],
    },
  ].map((group) => ({
    ...group,
    items: group.items.filter((item) => item.minRole !== "admin" || isAdmin),
  })).filter((group) => group.items.length > 0);

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
            {managedServerEndpoint ? (
              <div className="rounded-lg border border-blue-500/20 bg-blue-500/5 px-3 py-2.5">
                <p className="text-[11px] font-semibold text-slate-300 uppercase tracking-wider">Servidor Cortex</p>
                <p className="mt-1 font-mono text-xs text-blue-300 break-all">{serverUrl}</p>
                <p className="mt-1 text-[11px] text-slate-400">Configurado automáticamente por Docker Compose.</p>
              </div>
            ) : (
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
            )}

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

  const renderNavContent = () => (
    <>
      {/* Brand Header */}
      <div className="p-4 sm:p-5 border-b border-[var(--border-subtle)] flex items-center justify-between">
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

        <div className="flex items-center gap-1">
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

          {/* Close button inside mobile drawer */}
          <Button
            ref={mobileCloseButtonRef}
            onClick={() => setMobileMenuOpen(false)}
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0 md:hidden text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            aria-label="Cerrar navegación"
          >
            <X className="h-5 w-5" />
          </Button>
        </div>
      </div>

      {/* User Identity Card */}
      <div className="px-3.5 py-2.5 mx-3 mt-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-center justify-between">
        <div className="flex items-center gap-2 overflow-hidden">
          <div className="w-7 h-7 rounded-full bg-blue-500/20 text-blue-400 flex items-center justify-center shrink-0">
            <User className="h-3.5 w-3.5" />
          </div>
          <div className="overflow-hidden">
            <div className="text-xs font-semibold truncate">{displayName}</div>
            <div className="text-[9px] text-[var(--text-muted)] uppercase tracking-wider">
              {primaryRole}
            </div>
          </div>
        </div>
        <Badge
          variant={isAdmin ? "default" : "secondary"}
          className="text-[9px] px-1.5 py-0 h-4 font-mono uppercase shrink-0"
        >
          {primaryRole}
        </Badge>
      </div>

      {/* Navigation List */}
      <nav className="flex-1 overflow-y-auto px-3 py-3" aria-label="Navegación principal">
        {navGroups.map((group) => (
          <section key={group.label} className="mb-4 last:mb-0" aria-labelledby={`nav-${group.label}`}>
            <h2 id={`nav-${group.label}`} className="mb-1.5 px-3 text-[10px] font-semibold uppercase tracking-[0.16em] text-[var(--text-muted)]">
              {group.label}
            </h2>
            <div className="space-y-1">
              {group.items.map((item) => {
                const Icon = item.icon;
                const isActive = pathname === item.href;
                return (
                  <Link
                    key={item.href}
                    href={item.href}
                    aria-current={isActive ? "page" : undefined}
                    onClick={() => setMobileMenuOpen(false)}
                    className={`flex items-center justify-between rounded-lg px-3.5 py-2.5 text-xs font-medium transition-all duration-150 ${
                      isActive
                        ? "bg-[var(--accent-primary)] font-semibold text-white shadow-md shadow-blue-600/20"
                        : "text-[var(--text-secondary)] hover:bg-[var(--bg-surface-hover)] hover:text-[var(--text-primary)]"
                    }`}
                  >
                    <span className="flex items-center gap-2.5">
                      <Icon className={`h-4 w-4 shrink-0 ${isActive ? "text-white" : "text-[var(--text-secondary)]"}`} aria-hidden="true" />
                      <span>{item.label}</span>
                    </span>
                    {item.badge ? (
                      <span className={`shrink-0 rounded-full px-1.5 py-0.5 font-mono text-[9px] font-semibold ${
                        isActive ? "bg-white/20 text-white" : "border border-[var(--border-subtle)] bg-[var(--bg-surface)] text-[var(--text-muted)]"
                      }`}>{item.badge}</span>
                    ) : null}
                  </Link>
                );
              })}
            </div>
          </section>
        ))}
      </nav>

      {/* Sidebar Footer / System Status */}
      <div className="p-3.5 border-t border-[var(--border-subtle)] bg-[var(--bg-surface)] space-y-3">
        {/* User Identity & Role Card */}
        <div className="p-3 rounded-lg bg-[var(--bg-surface)] border border-[var(--border-subtle)] space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <div className="w-7 h-7 rounded-full bg-blue-500/10 border border-blue-500/30 flex items-center justify-center text-blue-400 font-bold text-xs shrink-0">
                {displayName.charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0">
                <div className="text-xs font-semibold text-[var(--text-primary)] truncate">
                  {displayName}
                </div>
                {userEmail ? (
                  <div className="text-[10px] text-[var(--text-muted)] truncate">
                    {userEmail}
                  </div>
                ) : null}
              </div>
            </div>
            <Badge
              variant="outline"
              className={`text-[9px] px-1.5 py-0 uppercase font-mono tracking-wider shrink-0 ${
                isAdmin
                  ? "border-purple-500/40 text-purple-400 bg-purple-500/10"
                  : isDeveloper
                  ? "border-blue-500/40 text-blue-400 bg-blue-500/10"
                  : "border-slate-500/40 text-slate-400 bg-slate-500/10"
              }`}
            >
              {primaryRole}
            </Badge>
          </div>

          <div className="pt-1 border-t border-[var(--border-subtle)]/50 flex items-center justify-between text-[10px]">
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
              <span className="text-emerald-400 font-medium">
                {serverUrl.includes("railway") || serverUrl.includes("http") ? "PostgreSQL Cloud Node" : "Local SQLite Node"}
              </span>
            </div>
            {principal?.id ? (
              <span className="text-[9px] text-[var(--text-muted)] font-mono">
                {principal.id.slice(0, 6)}...
              </span>
            ) : null}
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
    </>
  );

  return (
    <div className="flex min-h-screen bg-[var(--bg-primary)] text-[var(--text-primary)] antialiased font-sans transition-colors duration-200">
      {/* Mobile Drawer Backdrop & Overlay */}
      {mobileMenuOpen && (
        <div
          className="fixed inset-0 bg-black/60 backdrop-blur-sm z-40 md:hidden transition-opacity"
          onClick={() => setMobileMenuOpen(false)}
        />
      )}

      {/* Mobile Slide-over Drawer */}
      <aside
        id="mobile-navigation"
        ref={mobileDrawerRef}
        role="dialog"
        aria-modal="true"
        aria-label="Navegación móvil"
        inert={!mobileMenuOpen}
        className={`fixed top-0 bottom-0 left-0 w-72 max-w-[85vw] bg-[var(--bg-secondary)] border-r border-[var(--border-subtle)] flex flex-col z-50 md:hidden transition-transform duration-300 ease-in-out ${
          mobileMenuOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        {renderNavContent()}
      </aside>

      {/* Desktop Sleek Sidebar */}
      <aside className="hidden md:flex w-64 bg-[var(--bg-secondary)] border-r border-[var(--border-subtle)] flex-col shrink-0 sticky top-0 h-screen backdrop-blur-md z-30">
        {renderNavContent()}
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0 overflow-y-auto">
        {/* Top Bar */}
        <header className="min-h-16 py-2.5 bg-[var(--bg-secondary)] backdrop-blur-md border-b border-[var(--border-subtle)] flex flex-wrap items-center justify-between px-3 sm:px-5 md:px-7 sticky top-0 z-20 gap-2">
          <div className="flex items-center gap-2 sm:gap-3 flex-wrap">
            {/* Mobile Hamburger Toggle */}
            <Button
              ref={mobileMenuButtonRef}
              onClick={() => setMobileMenuOpen(true)}
              variant="ghost"
              size="sm"
              className="h-8 w-8 p-0 md:hidden text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              title="Abrir Menú"
              aria-label="Abrir navegación"
              aria-controls="mobile-navigation"
              aria-expanded={mobileMenuOpen}
            >
              <Menu className="h-5 w-5" />
            </Button>

            <Badge variant="secondary" className="gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)] max-w-[260px] sm:max-w-sm truncate">
              <Server className="h-3 w-3 text-blue-400 shrink-0" />
              <span className="font-mono text-[11px] truncate">{serverUrl}</span>
              {latencyMs !== null ? (
                <span className="inline-flex items-center gap-1 font-mono text-[10px] text-emerald-400 pl-1.5 border-l border-[var(--border-subtle)]">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse" />
                  {latencyMs}ms
                </span>
              ) : null}
            </Badge>

            {grantedWorkspaces.length ? (
              <label className="hidden sm:flex items-center gap-1 rounded-md border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-2 py-1 text-[10px] text-[var(--text-secondary)]">
                <span className="font-semibold uppercase tracking-wide">Workspace</span>
                <select
                  value={selectedWorkspace}
                  onChange={changeWorkspace}
                  className="max-w-36 bg-transparent font-mono text-[11px] text-[var(--text-primary)] outline-none"
                  aria-label="Workspace activo"
                >
                  {grantedWorkspaces.map((id) => (
                    <option key={id} value={id}>
                      {`WS ${id.slice(0, 8)}`}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}

            <Badge
              variant={cloudSyncEnabled ? "default" : "outline"}
              className="text-[10px] cursor-pointer"
              onClick={() => setCloudSyncEnabled(!cloudSyncEnabled)}
            >
              {cloudSyncEnabled ? "☁️ Sync" : "🔒 Local"}
            </Badge>
          </div>

          <div className="flex items-center gap-1.5 sm:gap-2.5 ml-auto">
            {/* Theme Toggle Button */}
            <Button
              onClick={toggleTheme}
              variant="outline"
              size="sm"
              className="h-8 px-2 sm:px-2.5 text-xs gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            >
              {isLightMode ? (
                <>
                  <Moon className="h-3.5 w-3.5" />
                  <span className="hidden sm:inline">Oscuro</span>
                </>
              ) : (
                <>
                  <Sun className="h-3.5 w-3.5 text-amber-400" />
                  <span className="hidden sm:inline">Claro</span>
                </>
              )}
            </Button>

            <Button
              onClick={() => refreshState()}
              variant="outline"
              size="sm"
              className="h-8 px-2 sm:px-2.5 text-xs gap-1.5 bg-[var(--bg-surface)] border-[var(--border-subtle)] text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
              title="Refrescar estado"
            >
              <RefreshCw className="h-3.5 w-3.5" />
              <span className="hidden sm:inline">Refrescar</span>
            </Button>
          </div>
        </header>

        {/* Content Body Container */}
        <main className="p-3 sm:p-5 md:p-7 max-w-7xl w-full mx-auto space-y-6">{children}</main>
      </div>
    </div>
  );
}
