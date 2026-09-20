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
      <div className="min-h-screen flex items-center justify-center bg-background text-foreground p-4">
        <Card className="max-w-md w-full p-6 sm:p-8 shadow-sm border-border bg-card text-card-foreground">
          <div className="text-center mb-6">
            <div className="w-12 h-12 rounded-lg bg-primary/10 border border-primary/20 text-primary flex items-center justify-center mx-auto mb-4">
              <BrainCircuit className="h-6 w-6" />
            </div>
            <h1 className="text-lg font-bold tracking-tight text-card-foreground uppercase">Cortex Control Room</h1>
            <p className="text-xs text-muted-foreground mt-1">
              Memoria persistente y arquitectura cognitiva para coding agents
            </p>
          </div>

          {(connectError || error) && (
            <div className="bg-destructive/10 border border-destructive/30 text-destructive p-3 rounded-lg text-xs mb-5 flex items-center gap-2.5">
              <AlertCircle className="h-4 w-4 shrink-0" />
              <span>{connectError || error}</span>
            </div>
          )}

          <form onSubmit={handleConnect} className="space-y-4 text-xs">
            {managedServerEndpoint ? (
              <div className="rounded-lg border border-primary/20 bg-primary/5 px-3 py-2.5">
                <p className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">Servidor Cortex</p>
                <p className="mt-1 font-mono text-xs text-primary break-all">{serverUrl}</p>
                <p className="mt-1 text-[11px] text-muted-foreground">Configurado automáticamente por Docker Compose.</p>
              </div>
            ) : (
              <div className="space-y-1.5">
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase tracking-wider">
                  CORTEX SERVER ENDPOINT
                </label>
                <Input
                  type="text"
                  value={inputUrl}
                  onChange={(e) => setInputUrl(e.target.value)}
                  placeholder="http://localhost:7438"
                  required
                  className="h-10 text-xs font-mono"
                />
              </div>
            )}

            <div className="space-y-1.5">
              <label className="text-[11px] font-semibold text-muted-foreground block uppercase tracking-wider">
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
                className="h-10 text-xs font-mono"
              />
            </div>

            <Button
              type="submit"
              disabled={isSubmitting}
              className="w-full h-10 mt-2 text-xs font-semibold shadow-sm"
            >
              {isSubmitting ? "Conectando..." : "Conectar con Token"}
            </Button>
          </form>

          <div className="mt-6 pt-5 border-t border-border text-center flex items-center justify-between">
            <p className="text-[11px] text-muted-foreground text-left">
              Soporta tokens <code className="text-foreground font-mono font-medium">admin</code> y <code className="text-foreground font-mono font-medium">member</code>.
            </p>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={toggleTheme}
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              {isLightMode ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4 text-amber-500" />}
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  const renderNavContent = () => (
    <>
      {/* Brand Header */}
      <div className="p-4 sm:p-5 border-b border-border flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="w-9 h-9 rounded-lg bg-primary text-primary-foreground flex items-center justify-center shadow-sm">
            <BrainCircuit className="h-5 w-5" />
          </div>
          <div>
            <div className="font-bold text-sm tracking-tight flex items-center gap-1.5">
              <span>CORTEX</span>
              <Badge variant="secondary" className="text-[9px] px-1.5 py-0 h-4 font-mono">v2.0</Badge>
            </div>
            <div className="text-[10px] text-muted-foreground font-medium">Cognitive Memory Plane</div>
          </div>
        </div>

        <div className="flex items-center gap-1">
          {/* Theme Toggle Button */}
          <Button
            onClick={toggleTheme}
            variant="ghost"
            size="sm"
            className="h-8 w-8 p-0 text-muted-foreground hover:text-foreground"
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
            className="h-8 w-8 p-0 md:hidden text-muted-foreground hover:text-foreground"
            aria-label="Cerrar navegación"
          >
            <X className="h-5 w-5" />
          </Button>
        </div>
      </div>

      {/* User Identity Card */}
      <div className="px-3.5 py-2.5 mx-3 mt-3 rounded-lg bg-secondary/60 border border-border flex items-center justify-between">
        <div className="flex items-center gap-2 overflow-hidden">
          <div className="w-7 h-7 rounded-md bg-primary/10 text-primary flex items-center justify-center shrink-0">
            <User className="h-3.5 w-3.5" />
          </div>
          <div className="overflow-hidden">
            <div className="text-xs font-semibold truncate">{displayName}</div>
            <div className="text-[9px] text-muted-foreground uppercase tracking-wider">
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
            <h2 id={`nav-${group.label}`} className="mb-1.5 px-3 text-[10px] font-semibold uppercase tracking-[0.16em] text-muted-foreground">
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
                    className={`flex items-center justify-between rounded-lg px-3.5 py-2.5 text-xs font-medium transition-colors duration-150 ${
                      isActive
                        ? "bg-primary font-semibold text-primary-foreground shadow-sm"
                        : "text-muted-foreground hover:bg-accent hover:text-foreground"
                    }`}
                  >
                    <span className="flex items-center gap-2.5">
                      <Icon className={`h-4 w-4 shrink-0 ${isActive ? "text-primary-foreground" : "text-muted-foreground"}`} aria-hidden="true" />
                      <span>{item.label}</span>
                    </span>
                    {item.badge ? (
                      <span className={`shrink-0 rounded-md px-1.5 py-0.5 font-mono text-[9px] font-semibold ${
                        isActive ? "bg-primary-foreground/20 text-primary-foreground" : "border border-border bg-secondary text-muted-foreground"
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
      <div className="p-3.5 border-t border-border space-y-3">
        {/* User Identity & Role Card */}
        <div className="p-3 rounded-lg bg-secondary/60 border border-border space-y-2">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 min-w-0">
              <div className="w-7 h-7 rounded-md bg-primary/10 border border-primary/20 flex items-center justify-center text-primary font-bold text-xs shrink-0">
                {displayName.charAt(0).toUpperCase()}
              </div>
              <div className="min-w-0">
                <div className="text-xs font-semibold text-foreground truncate">
                  {displayName}
                </div>
                {userEmail ? (
                  <div className="text-[10px] text-muted-foreground truncate">
                    {userEmail}
                  </div>
                ) : null}
              </div>
            </div>
            <Badge
              variant="outline"
              className={`text-[9px] px-1.5 py-0 uppercase font-mono tracking-wider shrink-0 ${
                isAdmin
                  ? "border-slate-600/40 text-slate-300 bg-slate-800/60"
                  : isDeveloper
                  ? "border-primary/30 text-primary bg-primary/10"
                  : "border-border text-muted-foreground bg-secondary"
              }`}
            >
              {primaryRole}
            </Badge>
          </div>

          <div className="pt-1 border-t border-border/40 flex items-center justify-between text-[10px]">
            <div className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-emerald-500" />
              <span className="text-emerald-600 dark:text-emerald-400 font-medium">
                {serverUrl.includes("railway") || serverUrl.includes("http") ? "PostgreSQL Cloud Node" : "Local SQLite Node"}
              </span>
            </div>
            {principal?.id ? (
              <span className="text-[9px] text-muted-foreground font-mono">
                {principal.id.slice(0, 6)}...
              </span>
            ) : null}
          </div>
        </div>

        <Button
          onClick={logout}
          variant="ghost"
          size="sm"
          className="w-full justify-center text-xs text-muted-foreground hover:text-destructive hover:bg-destructive/10 h-8"
        >
          <LogOut className="h-3.5 w-3.5 mr-1.5" />
          <span>Cerrar Sesión</span>
        </Button>
      </div>
    </>
  );

  return (
    <div className="flex min-h-screen bg-background text-foreground antialiased font-sans transition-colors duration-200">
      {/* Mobile Drawer Backdrop & Overlay */}
      {mobileMenuOpen && (
        <div
          className="fixed inset-0 bg-black/60 z-40 md:hidden transition-opacity"
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
        className={`fixed top-0 bottom-0 left-0 w-72 max-w-[85vw] bg-card border-r border-border flex flex-col z-50 md:hidden transition-transform duration-300 ease-in-out ${
          mobileMenuOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        {renderNavContent()}
      </aside>

      {/* Desktop Sleek Sidebar */}
      <aside className="hidden md:flex w-64 bg-card border-r border-border flex-col shrink-0 sticky top-0 h-screen z-30">
        {renderNavContent()}
      </aside>

      {/* Main Content Area */}
      <div className="flex-1 flex flex-col min-w-0 overflow-y-auto">
        {/* Top Bar */}
        <header className="min-h-16 py-2.5 bg-card border-b border-border flex flex-wrap items-center justify-between px-3 sm:px-5 md:px-7 sticky top-0 z-20 gap-2">
          <div className="flex items-center gap-2 sm:gap-3 flex-wrap">
            {/* Mobile Hamburger Toggle */}
            <Button
              ref={mobileMenuButtonRef}
              onClick={() => setMobileMenuOpen(true)}
              variant="ghost"
              size="sm"
              className="h-8 w-8 p-0 md:hidden text-muted-foreground hover:text-foreground"
              title="Abrir Menú"
              aria-label="Abrir navegación"
              aria-controls="mobile-navigation"
              aria-expanded={mobileMenuOpen}
            >
              <Menu className="h-5 w-5" />
            </Button>

            <Badge variant="secondary" className="gap-1.5 bg-secondary border-border text-muted-foreground max-w-[260px] sm:max-w-sm truncate">
              <Server className="h-3 w-3 text-primary shrink-0" />
              <span className="font-mono text-[11px] truncate">{serverUrl}</span>
              {latencyMs !== null ? (
                <span className="inline-flex items-center gap-1 font-mono text-[10px] text-emerald-500 dark:text-emerald-400 pl-1.5 border-l border-border">
                  <span className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                  {latencyMs}ms
                </span>
              ) : null}
            </Badge>

            {grantedWorkspaces.length ? (
              <label className="hidden sm:flex items-center gap-1 rounded-md border border-border bg-secondary px-2 py-1 text-[10px] text-muted-foreground">
                <span className="font-semibold uppercase tracking-wide">Workspace</span>
                <select
                  value={selectedWorkspace}
                  onChange={changeWorkspace}
                  className="max-w-36 bg-transparent font-mono text-[11px] text-foreground outline-none"
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
              className="h-8 px-2 sm:px-2.5 text-xs gap-1.5 bg-secondary border-border text-muted-foreground hover:text-foreground"
            >
              {isLightMode ? (
                <>
                  <Moon className="h-3.5 w-3.5" />
                  <span className="hidden sm:inline">Oscuro</span>
                </>
              ) : (
                <>
                  <Sun className="h-3.5 w-3.5 text-amber-500" />
                  <span className="hidden sm:inline">Claro</span>
                </>
              )}
            </Button>

            <Button
              onClick={() => refreshState()}
              variant="outline"
              size="sm"
              className="h-8 px-2 sm:px-2.5 text-xs gap-1.5 bg-secondary border-border text-muted-foreground hover:text-foreground"
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
