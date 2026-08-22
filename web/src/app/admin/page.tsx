"use client";

import React, { useEffect, useState, useMemo } from "react";
import Link from "next/link";
import { useAuth } from "@/lib/auth-context";
import { User, Token } from "@/lib/api";
import {
  generateClaudeDesktopConfig,
  generateCursorMcpConfig,
  generateWindsurfConfig,
  generateVSCodeClineConfig,
  generateOpenCodeConfig,
  generateCortexYaml,
  generateEnvFile,
  generateQuickstartScript,
  downloadFile,
  AgentExportContext,
} from "@/lib/config-exporter";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Dialog, DialogHeader, DialogTitle, DialogClose } from "@/components/ui/dialog";
import {
  ShieldCheck,
  Key,
  Users,
  Bot,
  Plus,
  Download,
  Copy,
  Trash2,
  CheckCircle,
  FileCode,
  Terminal,
  X,
  Check,
  Search,
  Filter,
  Sparkles,
  Layers,
  Cpu,
  Lock,
  Radio,
  ExternalLink,
  Code2,
  FolderGit2,
  RefreshCw,
  SlidersHorizontal,
} from "lucide-react";

// Predefined AI Agent client profiles for the MCP Integration Hub
const AGENT_PROFILES = [
  {
    id: "claude-desktop",
    name: "Claude Desktop",
    icon: "🟣",
    description: "App nativa de Anthropic con integración Streamable HTTP MCP",
    format: "JSON",
    filename: "claude_desktop_config.json",
    generator: generateClaudeDesktopConfig,
  },
  {
    id: "cursor",
    name: "Cursor IDE",
    icon: "⚡",
    description: "Editor basado en VS Code con soporte MCP para Composer y Chat",
    format: "JSON",
    filename: "cursor_mcp.json",
    generator: generateCursorMcpConfig,
  },
  {
    id: "windsurf",
    name: "Windsurf (Cascade)",
    icon: "🌊",
    description: "Entorno de desarrollo de Codeium con arquitectura MCP Cascade",
    format: "JSON",
    filename: "windsurf_config.json",
    generator: generateWindsurfConfig,
  },
  {
    id: "opencode",
    name: "OpenCode AI",
    icon: "🪐",
    description: "Plugin oficial de Cortex para OpenCode con schema remoto",
    format: "JSON",
    filename: "opencode.json",
    generator: generateOpenCodeConfig,
  },
  {
    id: "vscode-cline",
    name: "VS Code (Cline / Roo)",
    icon: "🤖",
    description: "Extensiones autónomas de codificación con soporte MCP Stdio/HTTP",
    format: "JSON",
    filename: "cline_mcp_settings.json",
    generator: generateVSCodeClineConfig,
  },
  {
    id: "cortex-cli",
    name: "Cortex CLI",
    icon: "🧠",
    description: "Archivo de configuración cortex.yaml para modo local y sync",
    format: "YAML",
    filename: "cortex.yaml",
    generator: generateCortexYaml,
  },
];

export default function AdminPage() {
  const { client, serverUrl, principal } = useAuth();

  const userRoles = principal?.roles || ["developer"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );

  // Data states
  const [users, setUsers] = useState<User[]>([]);
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);

  // Active Tab: "users" | "agents" | "tokens" | "integrations"
  const [activeTab, setActiveTab] = useState<"users" | "agents" | "tokens" | "integrations">("users");

  // Search & Filter
  const [searchQuery, setSearchQuery] = useState("");
  const [roleFilter, setRoleFilter] = useState<string>("all");
  const [statusFilter, setStatusFilter] = useState<string>("all");

  // Modals
  const [isUserModalOpen, setIsUserModalOpen] = useState(false);
  const [userEmail, setUserEmail] = useState("");
  const [userName, setUserName] = useState("");
  const [userRole, setUserRole] = useState("developer");

  const [isTokenModalOpen, setIsTokenModalOpen] = useState(false);
  const [tokenSubject, setTokenSubject] = useState("");
  const [tokenName, setTokenName] = useState("");
  const [tokenRolePreset, setTokenRolePreset] = useState("agent");
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);

  const [isDownloadModalOpen, setIsDownloadModalOpen] = useState(false);
  const [selectedTokenForExport, setSelectedTokenForExport] = useState<Token | null>(null);
  const [exportProject, setExportProject] = useState("default");

  // Integration Hub Interactive State
  const [selectedAgentProfileId, setSelectedAgentProfileId] = useState<string>("claude-desktop");
  const [scriptOs, setScriptOs] = useState<"sh" | "ps1">("ps1");
  const [copiedText, setCopiedText] = useState<string | null>(null);

  const fetchData = async () => {
    if (!client) return;
    setLoading(true);
    try {
      const [uList, tList] = await Promise.all([
        client.users().catch(() => []),
        client.tokens().catch(() => []),
      ]);
      setUsers(uList || []);
      setTokens(tList || []);
    } catch (err) {
      console.error("Failed to load admin data", err);
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  useEffect(() => {
    fetchData();
  }, [client]);

  const handleRefresh = () => {
    setRefreshing(true);
    fetchData();
  };

  // KPIs Calculations
  const stats = useMemo(() => {
    const totalUsers = users.length;
    const humanUsers = users.filter((u) => !u.roles?.includes("agent")).length;
    const aiAgents = users.filter((u) => u.roles?.includes("agent")).length;
    const activeTokens = tokens.filter((t) => !t.revoked_at).length;
    const revokedTokens = tokens.filter((t) => t.revoked_at).length;

    return {
      totalUsers,
      humanUsers,
      aiAgents,
      activeTokens,
      revokedTokens,
    };
  }, [users, tokens]);

  // Filtered Users
  const filteredUsers = useMemo(() => {
    return users.filter((u) => {
      // Exclude agents when in human users tab
      if (activeTab === "users" && u.roles?.includes("agent")) return false;
      // Exclude humans when in agents tab
      if (activeTab === "agents" && !u.roles?.includes("agent")) return false;

      const q = searchQuery.toLowerCase();
      const matchSearch =
        u.display_name.toLowerCase().includes(q) ||
        u.email.toLowerCase().includes(q) ||
        u.id.toLowerCase().includes(q);

      if (!matchSearch) return false;

      if (roleFilter !== "all") {
        if (!u.roles?.includes(roleFilter)) return false;
      }

      return true;
    });
  }, [users, activeTab, searchQuery, roleFilter]);

  // Filtered Tokens
  const filteredTokens = useMemo(() => {
    return tokens.filter((t) => {
      const q = searchQuery.toLowerCase();
      const matchSearch =
        t.name.toLowerCase().includes(q) ||
        t.subject.toLowerCase().includes(q) ||
        t.prefix.toLowerCase().includes(q) ||
        t.id.toLowerCase().includes(q);

      if (!matchSearch) return false;

      if (statusFilter === "active" && t.revoked_at) return false;
      if (statusFilter === "revoked" && !t.revoked_at) return false;

      return true;
    });
  }, [tokens, searchQuery, statusFilter]);

  // Handler: Create User / Agent
  const handleCreateUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client) return;
    try {
      await client.createUser({
        email: userEmail,
        display_name: userName,
        roles: [userRole],
      });
      setIsUserModalOpen(false);
      setUserEmail("");
      setUserName("");
      fetchData();
    } catch (err: any) {
      alert("Error al crear usuario o agente: " + (err.message || err));
    }
  };

  // Handler: Issue Token
  const handleIssueToken = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client) return;
    try {
      const scopes =
        tokenRolePreset === "admin"
          ? ["admin", "agent", "observations:read", "observations:write", "graph:read", "graph:write"]
          : ["agent", "observations:read", "observations:write", "graph:read", "graph:write"];

      const tok = await client.issueToken({
        subject: tokenSubject,
        name: tokenName,
        scopes,
      });
      setIssuedSecret(tok.secret || "Token creado exitosamente");
      fetchData();
    } catch (err: any) {
      alert("Error al emitir token: " + (err.message || err));
    }
  };

  // Handler: Revoke Token
  const handleRevokeToken = async (id: string, name: string) => {
    if (!confirm(`¿Estás seguro de que deseas revocar permanentemente el token "${name}"? Los agentes que lo usen perderán el acceso de inmediato.`)) {
      return;
    }
    if (!client) return;
    try {
      await client.revokeToken(id);
      fetchData();
    } catch (err: any) {
      alert("Error al revocar token: " + (err.message || err));
    }
  };

  const copyToClipboard = (text: string, label: string) => {
    navigator.clipboard.writeText(text);
    setCopiedText(label);
    setTimeout(() => setCopiedText(null), 2000);
  };

  const getExportContext = (customToken?: Token | null): AgentExportContext => {
    return {
      serverUrl,
      userEmail: customToken?.subject || selectedTokenForExport?.subject,
      tokenName: customToken?.name || selectedTokenForExport?.name,
      projectName: exportProject,
    };
  };

  const openExportModal = (t: Token) => {
    setSelectedTokenForExport(t);
    setIsDownloadModalOpen(true);
  };

  // Selected Profile for Integration Hub
  const selectedProfile = useMemo(() => {
    return AGENT_PROFILES.find((p) => p.id === selectedAgentProfileId) || AGENT_PROFILES[0];
  }, [selectedAgentProfileId]);

  const currentGeneratedConfig = useMemo(() => {
    try {
      return selectedProfile.generator(getExportContext());
    } catch (e: any) {
      return `// Error generando configuración: ${e.message}`;
    }
  }, [selectedProfile, serverUrl, exportProject]);

  const currentQuickstartScript = useMemo(() => {
    try {
      return generateQuickstartScript(getExportContext(), scriptOs);
    } catch (e: any) {
      return `# Error generando script: ${e.message}`;
    }
  }, [scriptOs, serverUrl, exportProject]);

  if (!isAdmin) {
    return (
      <div className="max-w-2xl mx-auto py-16 text-center space-y-4">
        <div className="w-14 h-14 rounded-2xl bg-amber-500/10 border border-amber-500/30 text-amber-400 flex items-center justify-center mx-auto shadow-lg shadow-amber-500/10">
          <ShieldCheck className="h-7 w-7" />
        </div>
        <h2 className="text-xl font-bold text-[var(--text-primary)]">Acceso Restringido</h2>
        <p className="text-xs text-[var(--text-secondary)] max-w-md mx-auto leading-relaxed">
          La gestión de identidades, agentes autónomos y generación de tokens de infraestructura están reservados para administradores.
        </p>
        <div className="pt-3">
          <Link href="/">
            <Button variant="outline" className="border-[var(--border-subtle)] text-xs">
              Volver al Dashboard
            </Button>
          </Link>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Top Header */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 p-5 rounded-2xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] shadow-xl">
        <div>
          <div className="flex items-center gap-2.5">
            <div className="p-2 rounded-xl bg-blue-500/10 border border-blue-500/20 text-blue-400">
              <ShieldCheck className="h-6 w-6" />
            </div>
            <div>
              <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text-primary)]">
                Centro de Administración y Agentes
              </h1>
              <p className="text-xs text-[var(--text-muted)] mt-0.5">
                Control de identidades, perfiles de Coding Agents, emisión de tokens y configuraciones MCP
              </p>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2 shrink-0">
          <Button
            onClick={handleRefresh}
            variant="outline"
            size="sm"
            disabled={refreshing}
            className="h-9 text-xs gap-1.5"
            title="Recargar datos"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`} />
            <span className="hidden sm:inline">Actualizar</span>
          </Button>

          <Button
            onClick={() => {
              setUserRole("developer");
              setIsUserModalOpen(true);
            }}
            variant="secondary"
            size="sm"
            className="h-9 gap-1.5 text-xs"
          >
            <Users className="h-3.5 w-3.5 text-blue-400" />
            <span>+ Usuario</span>
          </Button>

          <Button
            onClick={() => {
              setUserRole("agent");
              setIsUserModalOpen(true);
            }}
            variant="secondary"
            size="sm"
            className="h-9 gap-1.5 text-xs"
          >
            <Bot className="h-3.5 w-3.5 text-purple-400" />
            <span>+ Agente</span>
          </Button>

          <Button
            onClick={() => setIsTokenModalOpen(true)}
            size="sm"
            className="h-9 gap-1.5 text-xs bg-blue-600 hover:bg-blue-500 text-white shadow-lg shadow-blue-600/20"
          >
            <Key className="h-3.5 w-3.5" />
            <span>Emitir Token</span>
          </Button>
        </div>
      </div>

      {/* KPI Metrics Summary Cards */}
      <div className="grid grid-cols-2 lg:grid-cols-4 gap-3.5">
        <div className="p-4 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] flex items-center justify-between shadow-sm">
          <div className="space-y-1">
            <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
              Usuarios Humanos
            </span>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-[var(--text-primary)] font-mono">
                {stats.humanUsers}
              </span>
              <span className="text-[11px] text-[var(--text-muted)]">registrados</span>
            </div>
          </div>
          <div className="p-2.5 rounded-xl bg-blue-500/10 border border-blue-500/20 text-blue-400">
            <Users className="h-5 w-5" />
          </div>
        </div>

        <div className="p-4 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] flex items-center justify-between shadow-sm">
          <div className="space-y-1">
            <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
              Agentes de IA
            </span>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-purple-400 font-mono">
                {stats.aiAgents}
              </span>
              <span className="text-[11px] text-[var(--text-muted)]">autónomos</span>
            </div>
          </div>
          <div className="p-2.5 rounded-xl bg-purple-500/10 border border-purple-500/20 text-purple-400">
            <Bot className="h-5 w-5" />
          </div>
        </div>

        <div className="p-4 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] flex items-center justify-between shadow-sm">
          <div className="space-y-1">
            <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
              Tokens Activos
            </span>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-emerald-400 font-mono">
                {stats.activeTokens}
              </span>
              <span className="text-[11px] text-[var(--text-muted)]">credenciales</span>
            </div>
          </div>
          <div className="p-2.5 rounded-xl bg-emerald-500/10 border border-emerald-500/20 text-emerald-400">
            <Key className="h-5 w-5" />
          </div>
        </div>

        <div className="p-4 rounded-xl bg-[var(--bg-secondary)] border border-[var(--border-subtle)] flex items-center justify-between shadow-sm">
          <div className="space-y-1">
            <span className="text-[11px] font-semibold text-[var(--text-muted)] uppercase tracking-wider block">
              Tokens Revocados
            </span>
            <div className="flex items-baseline gap-2">
              <span className="text-2xl font-bold text-slate-400 font-mono">
                {stats.revokedTokens}
              </span>
              <span className="text-[11px] text-[var(--text-muted)]">inactivos</span>
            </div>
          </div>
          <div className="p-2.5 rounded-xl bg-slate-500/10 border border-slate-500/20 text-slate-400">
            <Lock className="h-5 w-5" />
          </div>
        </div>
      </div>

      {/* Navigation Tabs Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3 p-1.5 bg-[var(--bg-secondary)] border border-[var(--border-subtle)] rounded-xl">
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => {
              setActiveTab("users");
              setRoleFilter("all");
            }}
            className={`flex items-center gap-2 px-3.5 py-2 text-xs font-semibold rounded-lg transition-all ${
              activeTab === "users"
                ? "bg-blue-600 text-white shadow-md shadow-blue-600/20"
                : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface)]"
            }`}
          >
            <Users className="h-4 w-4" />
            <span>Usuarios Humanos</span>
            <Badge
              variant={activeTab === "users" ? "default" : "secondary"}
              className="text-[10px] px-1.5 py-0"
            >
              {stats.humanUsers}
            </Badge>
          </button>

          <button
            type="button"
            onClick={() => {
              setActiveTab("agents");
              setRoleFilter("all");
            }}
            className={`flex items-center gap-2 px-3.5 py-2 text-xs font-semibold rounded-lg transition-all ${
              activeTab === "agents"
                ? "bg-purple-600 text-white shadow-md shadow-purple-600/20"
                : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface)]"
            }`}
          >
            <Bot className="h-4 w-4" />
            <span>Agentes de IA</span>
            <Badge
              variant={activeTab === "agents" ? "purple" : "secondary"}
              className="text-[10px] px-1.5 py-0"
            >
              {stats.aiAgents}
            </Badge>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("tokens")}
            className={`flex items-center gap-2 px-3.5 py-2 text-xs font-semibold rounded-lg transition-all ${
              activeTab === "tokens"
                ? "bg-emerald-600 text-white shadow-md shadow-emerald-600/20"
                : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface)]"
            }`}
          >
            <Key className="h-4 w-4" />
            <span>Tokens & Credenciales MCP</span>
            <Badge
              variant={activeTab === "tokens" ? "success" : "secondary"}
              className="text-[10px] px-1.5 py-0"
            >
              {stats.activeTokens}
            </Badge>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("integrations")}
            className={`flex items-center gap-2 px-3.5 py-2 text-xs font-semibold rounded-lg transition-all ${
              activeTab === "integrations"
                ? "bg-amber-600 text-white shadow-md shadow-amber-600/20"
                : "text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-surface)]"
            }`}
          >
            <Sparkles className="h-4 w-4 text-amber-300" />
            <span>Hub de Integración MCP</span>
          </button>
        </div>

        {/* Global Search Bar when in list tabs */}
        {activeTab !== "integrations" && (
          <div className="relative min-w-[200px] sm:min-w-[260px] mr-1">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-[var(--text-muted)]" />
            <Input
              type="text"
              placeholder={`Buscar en ${activeTab === "users" ? "usuarios" : activeTab === "agents" ? "agentes" : "tokens"}...`}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              className="h-8 pl-8 pr-7 text-xs bg-[var(--bg-surface)]"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={() => setSearchQuery("")}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-[var(--text-muted)] hover:text-white"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        )}
      </div>

      {/* TAB 1: USUARIOS HUMANOS */}
      {activeTab === "users" && (
        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
            <div>
              <h2 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                <Users className="h-4 w-4 text-blue-400" />
                <span>Usuarios y Desarrolladores del Sistema</span>
              </h2>
              <p className="text-[11px] text-[var(--text-muted)]">
                Cuentas de desarrolladores y administradores con acceso a la plataforma Cortex
              </p>
            </div>

            <div className="flex items-center gap-2">
              <span className="text-[11px] text-[var(--text-muted)]">Rol:</span>
              <Select
                value={roleFilter}
                onChange={(e) => setRoleFilter(e.target.value)}
                className="h-7 text-xs min-w-[120px]"
              >
                <option value="all">Todos los Roles</option>
                <option value="developer">Developer</option>
                <option value="admin">Administrator</option>
              </Select>
            </div>
          </div>

          {loading ? (
            <div className="py-12 flex flex-col items-center justify-center gap-2 text-slate-400">
              <span className="h-5 w-5 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
              <span className="text-xs">Cargando usuarios...</span>
            </div>
          ) : filteredUsers.length === 0 ? (
            <div className="py-12 text-center space-y-2">
              <Users className="h-8 w-8 text-slate-600 mx-auto" />
              <p className="text-xs text-[var(--text-muted)]">
                {searchQuery ? "No se encontraron usuarios que coincidan con la búsqueda." : "No hay usuarios registrados."}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setUserRole("developer");
                  setIsUserModalOpen(true);
                }}
                className="text-xs mt-2"
              >
                + Crear Primer Usuario
              </Button>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
              {filteredUsers.map((u) => {
                const isAdmin = u.roles?.includes("admin");
                return (
                  <div
                    key={u.id}
                    className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] hover:border-blue-500/50 transition-all flex flex-col justify-between space-y-3"
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex items-center gap-2.5">
                        <div className="h-9 w-9 rounded-full bg-blue-600/10 border border-blue-500/20 text-blue-400 flex items-center justify-center font-bold text-xs uppercase">
                          {u.display_name.slice(0, 2)}
                        </div>
                        <div className="overflow-hidden">
                          <h3 className="font-semibold text-xs text-[var(--text-primary)] truncate">
                            {u.display_name}
                          </h3>
                          <p className="text-[11px] text-[var(--text-muted)] truncate font-mono">
                            {u.email}
                          </p>
                        </div>
                      </div>

                      <Badge variant={isAdmin ? "destructive" : "default"} className="capitalize text-[10px] shrink-0">
                        {u.roles?.join(", ") || "developer"}
                      </Badge>
                    </div>

                    <div className="pt-2 border-t border-[var(--border-subtle)] flex items-center justify-between text-[10px] text-[var(--text-muted)]">
                      <span className="font-mono truncate">ID: {u.id.slice(0, 12)}...</span>
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setTokenSubject(u.id);
                          setTokenName(`${u.display_name} CLI Token`);
                          setIsTokenModalOpen(true);
                        }}
                        className="h-6 px-2 text-[10px] text-blue-400 hover:text-blue-300 gap-1"
                      >
                        <Key className="h-3 w-3" />
                        <span>Emitir Token</span>
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      )}

      {/* TAB 2: AGENTES DE IA AUTÓNOMOS */}
      {activeTab === "agents" && (
        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
            <div>
              <h2 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                <Bot className="h-4 w-4 text-purple-400" />
                <span>Agentes de IA y Asistentes de Codificación</span>
              </h2>
              <p className="text-[11px] text-[var(--text-muted)]">
                Identidades dedicadas para agentes autónomos (Claude Code, Cursor, Windsurf, OpenCode)
              </p>
            </div>

            <Button
              onClick={() => {
                setUserRole("agent");
                setIsUserModalOpen(true);
              }}
              size="sm"
              className="h-8 text-xs bg-purple-600 hover:bg-purple-500 text-white gap-1.5"
            >
              <Bot className="h-3.5 w-3.5" />
              <span>Registrar Nuevo Agente</span>
            </Button>
          </div>

          {loading ? (
            <div className="py-12 flex flex-col items-center justify-center gap-2 text-slate-400">
              <span className="h-5 w-5 animate-spin rounded-full border-2 border-purple-500 border-t-transparent" />
              <span className="text-xs">Cargando agentes de IA...</span>
            </div>
          ) : filteredUsers.length === 0 ? (
            <div className="py-12 text-center space-y-2">
              <Bot className="h-8 w-8 text-purple-600 mx-auto" />
              <p className="text-xs text-[var(--text-muted)]">
                {searchQuery ? "No se encontraron agentes que coincidan con la búsqueda." : "No hay agentes de IA registrados aún."}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  setUserRole("agent");
                  setIsUserModalOpen(true);
                }}
                className="text-xs mt-2 text-purple-400 border-purple-800"
              >
                + Registrar Primer Agente
              </Button>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
              {filteredUsers.map((a) => (
                <div
                  key={a.id}
                  className="p-4 rounded-xl bg-[var(--bg-surface)] border border-purple-900/30 hover:border-purple-500/50 transition-all flex flex-col justify-between space-y-3"
                >
                  <div className="flex items-start justify-between gap-2">
                    <div className="flex items-center gap-2.5">
                      <div className="h-9 w-9 rounded-full bg-purple-600/15 border border-purple-500/30 text-purple-300 flex items-center justify-center font-bold text-xs">
                        <Bot className="h-4 w-4" />
                      </div>
                      <div className="overflow-hidden">
                        <h3 className="font-semibold text-xs text-[var(--text-primary)] truncate">
                          {a.display_name}
                        </h3>
                        <p className="text-[11px] text-purple-300/70 truncate font-mono">
                          {a.email}
                        </p>
                      </div>
                    </div>

                    <Badge variant="purple" className="text-[10px] shrink-0 font-mono">
                      Autonomous Agent
                    </Badge>
                  </div>

                  <div className="p-2.5 rounded-lg bg-[var(--bg-secondary)] border border-[var(--border-subtle)] text-[11px] space-y-1">
                    <div className="flex items-center justify-between text-[10px] text-[var(--text-muted)]">
                      <span>Scopes Autorizados:</span>
                      <span className="text-emerald-400 font-mono">Full MCP Streamable</span>
                    </div>
                    <div className="text-[10px] text-[var(--text-muted)] font-mono truncate">
                      Subject: {a.id}
                    </div>
                  </div>

                  <div className="pt-2 border-t border-[var(--border-subtle)] flex items-center justify-between">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => {
                        setTokenSubject(a.id);
                        setTokenName(`${a.display_name} MCP Bearer`);
                        setIsTokenModalOpen(true);
                      }}
                      className="h-7 text-[11px] gap-1"
                    >
                      <Key className="h-3 w-3 text-purple-400" />
                      <span>Emitir Token</span>
                    </Button>

                    <Button
                      size="sm"
                      variant="secondary"
                      onClick={() => {
                        setSelectedTokenForExport({
                          id: a.id,
                          name: a.display_name,
                          prefix: "ctx_agt",
                          subject: a.id,
                          principal_type: "agent",
                          scopes: ["agent"],
                          workspaces: ["*"],
                        });
                        setIsDownloadModalOpen(true);
                      }}
                      className="h-7 text-[11px] gap-1"
                    >
                      <Download className="h-3 w-3 text-blue-400" />
                      <span>Config MCP</span>
                    </Button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      )}

      {/* TAB 3: TOKENS & CREDENCIALES MCP */}
      {activeTab === "tokens" && (
        <Card className="p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-4">
          <div className="flex flex-wrap items-center justify-between gap-3 pb-3 border-b border-[var(--border-subtle)]">
            <div>
              <h2 className="text-sm font-bold text-[var(--text-primary)] flex items-center gap-2">
                <Key className="h-4 w-4 text-emerald-400" />
                <span>Tokens de Acceso y Credenciales MCP</span>
              </h2>
              <p className="text-[11px] text-[var(--text-muted)]">
                Bearer tokens autenticados para llamadas a la API y servidores de contexto MCP
              </p>
            </div>

            <div className="flex items-center gap-2">
              <span className="text-[11px] text-[var(--text-muted)]">Estado:</span>
              <Select
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value)}
                className="h-7 text-xs min-w-[110px]"
              >
                <option value="all">Todos</option>
                <option value="active">Activos</option>
                <option value="revoked">Revocados</option>
              </Select>
            </div>
          </div>

          {loading ? (
            <div className="py-12 flex flex-col items-center justify-center gap-2 text-slate-400">
              <span className="h-5 w-5 animate-spin rounded-full border-2 border-emerald-500 border-t-transparent" />
              <span className="text-xs">Cargando tokens...</span>
            </div>
          ) : filteredTokens.length === 0 ? (
            <div className="py-12 text-center space-y-2">
              <Key className="h-8 w-8 text-slate-600 mx-auto" />
              <p className="text-xs text-[var(--text-muted)]">
                {searchQuery ? "No se encontraron tokens que coincidan con la búsqueda." : "No hay tokens emitidos."}
              </p>
              <Button
                size="sm"
                variant="outline"
                onClick={() => setIsTokenModalOpen(true)}
                className="text-xs mt-2"
              >
                + Emitir Primer Token
              </Button>
            </div>
          ) : (
            <div className="space-y-2.5">
              {filteredTokens.map((tok) => {
                const isRevoked = Boolean(tok.revoked_at);
                return (
                  <div
                    key={tok.id}
                    className={`p-4 rounded-xl border transition-all flex flex-col md:flex-row md:items-center justify-between gap-3 ${
                      isRevoked
                        ? "bg-[var(--bg-surface)]/50 border-red-900/30 opacity-70"
                        : "bg-[var(--bg-surface)] border-[var(--border-subtle)] hover:border-emerald-500/40"
                    }`}
                  >
                    <div className="space-y-1.5 overflow-hidden">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-xs text-[var(--text-primary)] truncate">
                          {tok.name}
                        </span>
                        <Badge
                          variant={isRevoked ? "destructive" : "success"}
                          className="text-[10px] font-mono shrink-0"
                        >
                          {isRevoked ? "Revocado" : "Activo"}
                        </Badge>
                      </div>

                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-[var(--text-muted)]">
                        <span className="flex items-center gap-1 font-mono">
                          Prefijo:{" "}
                          <code className="text-emerald-400 font-semibold px-1 py-0.5 bg-black/30 rounded">
                            {tok.prefix}...
                          </code>
                          <button
                            type="button"
                            onClick={() => copyToClipboard(tok.prefix, tok.id)}
                            className="text-slate-400 hover:text-white p-0.5"
                            title="Copiar prefijo"
                          >
                            {copiedText === tok.id ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
                          </button>
                        </span>

                        <span>•</span>
                        <span>
                          Subject: <code className="font-mono text-[var(--text-secondary)]">{tok.subject.slice(0, 10)}...</code>
                        </span>

                        {tok.scopes && tok.scopes.length > 0 && (
                          <>
                            <span>•</span>
                            <span className="text-[10px] text-slate-400">
                              Scopes: {tok.scopes.join(", ")}
                            </span>
                          </>
                        )}
                      </div>
                    </div>

                    <div className="flex items-center gap-2 shrink-0 pt-2 md:pt-0 border-t md:border-t-0 border-[var(--border-subtle)]">
                      <Button
                        onClick={() => openExportModal(tok)}
                        variant="secondary"
                        size="sm"
                        className="h-8 text-xs gap-1.5"
                      >
                        <Download className="h-3.5 w-3.5 text-blue-400" />
                        <span>Exportar Config</span>
                      </Button>

                      {!isRevoked && (
                        <Button
                          onClick={() => handleRevokeToken(tok.id, tok.name)}
                          variant="outline"
                          size="sm"
                          className="h-8 text-xs gap-1.5 text-red-400 border-red-900/50 hover:bg-red-950/40"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          <span>Revocar</span>
                        </Button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      )}

      {/* TAB 4: HUB DE INTEGRACIÓN MCP (CENTRO DE CONFIGURACIÓN) */}
      {activeTab === "integrations" && (
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          {/* Left Column: Client Selector List */}
          <div className="space-y-3">
            <Card className="p-4 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-3">
              <div className="flex items-center justify-between pb-2 border-b border-[var(--border-subtle)]">
                <span className="text-xs font-bold text-[var(--text-primary)] uppercase tracking-wider flex items-center gap-1.5">
                  <SlidersHorizontal className="h-3.5 w-3.5 text-amber-400" />
                  Seleccionar Cliente / IDE
                </span>
              </div>

              <div className="space-y-1.5">
                {AGENT_PROFILES.map((profile) => {
                  const isSelected = profile.id === selectedAgentProfileId;
                  return (
                    <button
                      key={profile.id}
                      type="button"
                      onClick={() => setSelectedAgentProfileId(profile.id)}
                      className={`w-full text-left p-3 rounded-xl border transition-all flex items-start justify-between gap-2 ${
                        isSelected
                          ? "bg-amber-500/10 border-amber-500/50 shadow-sm"
                          : "bg-[var(--bg-surface)] border-[var(--border-subtle)] hover:border-amber-500/30"
                      }`}
                    >
                      <div className="flex items-start gap-2.5">
                        <span className="text-lg leading-none">{profile.icon}</span>
                        <div>
                          <div className="font-semibold text-xs text-[var(--text-primary)]">
                            {profile.name}
                          </div>
                          <div className="text-[10px] text-[var(--text-muted)] line-clamp-1">
                            {profile.description}
                          </div>
                        </div>
                      </div>

                      <Badge variant="secondary" className="text-[9px] font-mono shrink-0">
                        {profile.format}
                      </Badge>
                    </button>
                  );
                })}
              </div>

              {/* Project parameter for config generation */}
              <div className="pt-2 border-t border-[var(--border-subtle)] space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-muted)] block uppercase">
                  Proyecto Objetivo:
                </label>
                <Input
                  type="text"
                  value={exportProject}
                  onChange={(e) => setExportProject(e.target.value)}
                  placeholder="default"
                  className="h-8 text-xs font-mono"
                />
              </div>
            </Card>
          </div>

          {/* Right Column: Code Preview and Setup Scripts */}
          <div className="lg:col-span-2 space-y-4">
            {/* Configuration File Box */}
            <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2 pb-2 border-b border-[var(--border-subtle)]">
                <div className="flex items-center gap-2">
                  <span className="text-base">{selectedProfile.icon}</span>
                  <div>
                    <h3 className="font-bold text-xs text-[var(--text-primary)]">
                      Archivo de Configuración para {selectedProfile.name}
                    </h3>
                    <p className="text-[10px] text-[var(--text-muted)] font-mono">
                      {selectedProfile.filename}
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => copyToClipboard(currentGeneratedConfig, "config-code")}
                    className="h-7 text-xs gap-1.5"
                  >
                    {copiedText === "config-code" ? (
                      <Check className="h-3 w-3 text-emerald-400" />
                    ) : (
                      <Copy className="h-3 w-3" />
                    )}
                    <span>{copiedText === "config-code" ? "Copiado" : "Copiar Config"}</span>
                  </Button>

                  <Button
                    size="sm"
                    onClick={() =>
                      downloadFile(
                        selectedProfile.filename,
                        currentGeneratedConfig,
                        selectedProfile.format === "YAML" ? "text/yaml" : "application/json",
                      )
                    }
                    className="h-7 text-xs gap-1.5 bg-blue-600 hover:bg-blue-500 text-white"
                  >
                    <Download className="h-3 w-3" />
                    <span>Descargar Archivo</span>
                  </Button>
                </div>
              </div>

              {/* Code block */}
              <div className="relative rounded-xl bg-[#090d16] border border-[var(--border-subtle)] p-3.5 overflow-x-auto font-mono text-[11px] text-slate-200 leading-relaxed max-h-72">
                <pre>{currentGeneratedConfig}</pre>
              </div>

              <p className="text-[11px] text-slate-400 italic">
                * Nota de Seguridad: Este archivo utiliza variables de entorno (<code className="text-amber-400">CORTEX_REMOTE_TOKEN</code>) para no almacenar secretos en texto plano.
              </p>
            </Card>

            {/* Quickstart Script Box */}
            <Card className="p-4 sm:p-5 bg-[var(--bg-secondary)] border-[var(--border-subtle)] space-y-3">
              <div className="flex flex-wrap items-center justify-between gap-2 pb-2 border-b border-[var(--border-subtle)]">
                <div className="flex items-center gap-2">
                  <Terminal className="h-4 w-4 text-emerald-400" />
                  <div>
                    <h3 className="font-bold text-xs text-[var(--text-primary)]">
                      Script de Inicio Rápido & Verificación
                    </h3>
                    <p className="text-[10px] text-[var(--text-muted)]">
                      Prueba de conexión y configuración del entorno de terminal
                    </p>
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <div className="flex items-center p-0.5 bg-[var(--bg-surface)] border border-[var(--border-subtle)] rounded-lg">
                    <button
                      type="button"
                      onClick={() => setScriptOs("ps1")}
                      className={`px-2 py-0.5 text-[10px] font-mono rounded ${
                        scriptOs === "ps1" ? "bg-blue-600 text-white" : "text-slate-400"
                      }`}
                    >
                      PowerShell
                    </button>
                    <button
                      type="button"
                      onClick={() => setScriptOs("sh")}
                      className={`px-2 py-0.5 text-[10px] font-mono rounded ${
                        scriptOs === "sh" ? "bg-blue-600 text-white" : "text-slate-400"
                      }`}
                    >
                      Bash
                    </button>
                  </div>

                  <Button
                    size="sm"
                    variant="secondary"
                    onClick={() => copyToClipboard(currentQuickstartScript, "script-code")}
                    className="h-7 text-xs gap-1.5"
                  >
                    {copiedText === "script-code" ? (
                      <Check className="h-3 w-3 text-emerald-400" />
                    ) : (
                      <Copy className="h-3 w-3" />
                    )}
                    <span>{copiedText === "script-code" ? "Copiado" : "Copiar"}</span>
                  </Button>

                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      downloadFile(
                        scriptOs === "ps1" ? "setup-agent.ps1" : "setup-agent.sh",
                        currentQuickstartScript,
                        "text/plain",
                      )
                    }
                    className="h-7 text-xs gap-1.5"
                  >
                    <Download className="h-3 w-3" />
                    <span>Descargar Script</span>
                  </Button>
                </div>
              </div>

              <div className="relative rounded-xl bg-[#090d16] border border-[var(--border-subtle)] p-3.5 overflow-x-auto font-mono text-[11px] text-emerald-300 leading-relaxed max-h-56">
                <pre>{currentQuickstartScript}</pre>
              </div>
            </Card>
          </div>
        </div>
      )}

      {/* Modal: Create User / Agent */}
      <Dialog open={isUserModalOpen} onOpenChange={setIsUserModalOpen}>
        <DialogHeader>
          <DialogTitle>
            {userRole === "agent" ? (
              <Bot className="h-4 w-4 text-purple-400" />
            ) : (
              <Users className="h-4 w-4 text-blue-400" />
            )}
            <span>{userRole === "agent" ? "Registrar Nuevo Agente de IA" : "Crear Nuevo Usuario"}</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsUserModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleCreateUser} className="space-y-3.5 mt-4 text-xs">
          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              {userRole === "agent" ? "NOMBRE DEL AGENTE" : "NOMBRE COMPLETO"}
            </label>
            <Input
              type="text"
              value={userName}
              onChange={(e) => setUserName(e.target.value)}
              placeholder={userRole === "agent" ? "Ej: Claude Code Agent 01" : "Ej: Maria Perez"}
              required
            />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              CORREO ELECTRÓNICO / IDENTIFICADOR
            </label>
            <Input
              type="email"
              value={userEmail}
              onChange={(e) => setUserEmail(e.target.value)}
              placeholder={userRole === "agent" ? "claude-code@cortex.local" : "maria@empresa.com"}
              required
            />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
              ROL EN EL SISTEMA
            </label>
            <Select value={userRole} onChange={(e) => setUserRole(e.target.value)}>
              <option value="developer">Developer (Desarrollador)</option>
              <option value="agent">Autonomous Agent (Agente de IA)</option>
              <option value="admin">Administrator (Administrador)</option>
            </Select>
          </div>

          <div className="flex flex-wrap justify-end gap-2 pt-2 border-t border-[var(--border-subtle)]">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsUserModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm" className="bg-blue-600 hover:bg-blue-500 text-white">
              {userRole === "agent" ? "Registrar Agente" : "Crear Usuario"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Modal: Issue Token */}
      <Dialog
        open={isTokenModalOpen}
        onOpenChange={(open) => {
          setIsTokenModalOpen(open);
          if (!open) setIssuedSecret(null);
        }}
      >
        <DialogHeader>
          <DialogTitle>
            <Key className="h-4 w-4 text-emerald-400" />
            <span>Emitir Nuevo Bearer Token MCP</span>
          </DialogTitle>
          <DialogClose
            onClick={() => {
              setIsTokenModalOpen(false);
              setIssuedSecret(null);
            }}
          />
        </DialogHeader>

        {issuedSecret ? (
          <div className="space-y-4 mt-4 text-xs">
            <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 p-4 rounded-xl space-y-2">
              <span className="font-semibold block flex items-center gap-1.5">
                <CheckCircle className="h-4 w-4" /> ¡Token emitido con éxito!
              </span>
              <p className="text-[11px] text-[var(--text-secondary)]">
                Copia este secreto ahora. Por seguridad, no volverá a mostrarse en la plataforma:
              </p>
              <div className="p-3 bg-black/50 rounded-lg border border-emerald-500/40 font-mono text-[11px] text-emerald-300 break-all select-all shadow-inner">
                {issuedSecret}
              </div>
            </div>

            <div className="flex flex-wrap justify-end gap-2 pt-2 border-t border-[var(--border-subtle)]">
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  navigator.clipboard.writeText(issuedSecret);
                  setCopiedText("secret");
                  setTimeout(() => setCopiedText(null), 2000);
                }}
                className="gap-1.5"
              >
                {copiedText === "secret" ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                <span>{copiedText === "secret" ? "¡Copiado!" : "Copiar Secreto"}</span>
              </Button>
              <Button
                type="button"
                size="sm"
                onClick={() => {
                  setIsTokenModalOpen(false);
                  setIssuedSecret(null);
                }}
                className="bg-blue-600 hover:bg-blue-500 text-white"
              >
                Listo
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleIssueToken} className="space-y-3.5 mt-4 text-xs">
            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                USUARIO O AGENTE ASIGNADO (SUBJECT)
              </label>
              <Select
                value={tokenSubject}
                onChange={(e) => setTokenSubject(e.target.value)}
                required
              >
                <option value="">Selecciona usuario o agente...</option>
                {users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.roles?.includes("agent") ? "🤖 " : "👤 "}
                    {u.display_name} ({u.email})
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                NOMBRE DESCRIPTIVO DEL TOKEN
              </label>
              <Input
                type="text"
                value={tokenName}
                onChange={(e) => setTokenName(e.target.value)}
                placeholder="Ej: Claude Code Workstation Token"
                required
              />
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                PERFIL DE PERMISOS (SCOPES)
              </label>
              <Select
                value={tokenRolePreset}
                onChange={(e) => setTokenRolePreset(e.target.value)}
              >
                <option value="agent">Agent MCP (Lectura y Escritura de Memoria y Grafos)</option>
                <option value="admin">Admin Master (Acceso Administrativo Completo)</option>
              </Select>
            </div>

            <div className="flex justify-end gap-2 pt-2 border-t border-[var(--border-subtle)]">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => setIsTokenModalOpen(false)}
              >
                Cancelar
              </Button>
              <Button type="submit" size="sm" className="bg-emerald-600 hover:bg-emerald-500 text-white">
                Generar Token
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* Modal: Export Config by Token */}
      <Dialog
        open={isDownloadModalOpen && !!selectedTokenForExport}
        onOpenChange={setIsDownloadModalOpen}
      >
        <DialogHeader>
          <DialogTitle>
            <Download className="h-4 w-4 text-blue-400" />
            <span>Descargar Configuración para {selectedTokenForExport?.name}</span>
          </DialogTitle>
          <DialogClose onClick={() => setIsDownloadModalOpen(false)} />
        </DialogHeader>

        <div className="space-y-4 mt-4 text-xs">
          <p className="text-slate-400">
            Descarga el archivo de integración listo para usar en tu cliente de IA preferido:
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              PROYECTO OBJETIVO
            </label>
            <Input
              type="text"
              value={exportProject}
              onChange={(e) => setExportProject(e.target.value)}
              className="h-8 text-xs font-mono"
            />
          </div>

          <div className="grid grid-cols-2 gap-2.5 pt-2">
            <Button
              onClick={() => {
                const c = generateClaudeDesktopConfig(getExportContext(selectedTokenForExport));
                downloadFile("claude_desktop_config.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>🟣</span>
              <span>Claude Desktop</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateCursorMcpConfig(getExportContext(selectedTokenForExport));
                downloadFile("cursor_mcp.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>⚡</span>
              <span>Cursor IDE</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateWindsurfConfig(getExportContext(selectedTokenForExport));
                downloadFile("windsurf_config.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>🌊</span>
              <span>Windsurf</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateOpenCodeConfig(getExportContext(selectedTokenForExport));
                downloadFile("opencode.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>🪐</span>
              <span>OpenCode</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateCortexYaml(getExportContext(selectedTokenForExport));
                downloadFile("cortex.yaml", c, "text/yaml");
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>🧠</span>
              <span>cortex.yaml</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateEnvFile(getExportContext(selectedTokenForExport));
                downloadFile(".env.cortex", c, "text/plain");
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <span>📄</span>
              <span>Variables .env</span>
            </Button>
          </div>

          <div className="flex justify-end pt-2 border-t border-[var(--border-subtle)]">
            <Button onClick={() => setIsDownloadModalOpen(false)} size="sm">
              Cerrar
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
