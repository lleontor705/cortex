"use client";

import React, { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { User, Token } from "@/lib/api";
import {
  generateClaudeDesktopConfig,
  generateCursorMcpConfig,
  generateWindsurfConfig,
  generateCortexYaml,
  generateQuickstartScript,
  downloadFile,
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
  Plus,
  Download,
  Copy,
  Trash2,
  CheckCircle,
  FileCode,
  Terminal,
  X,
  Check,
} from "lucide-react";

export default function AdminPage() {
  const { client, serverUrl } = useAuth();

  const [users, setUsers] = useState<User[]>([]);
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(true);

  // Modals
  const [isUserModalOpen, setIsUserModalOpen] = useState(false);
  const [userEmail, setUserEmail] = useState("");
  const [userName, setUserName] = useState("");
  const [userRole, setUserRole] = useState("developer");

  const [isTokenModalOpen, setIsTokenModalOpen] = useState(false);
  const [tokenSubject, setTokenSubject] = useState("");
  const [tokenName, setTokenName] = useState("");
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);

  const [isDownloadModalOpen, setIsDownloadModalOpen] = useState(false);
  const [selectedTokenForExport, setSelectedTokenForExport] = useState<Token | null>(null);
  const [exportProject, setExportProject] = useState("default");
  const [copiedSecret, setCopiedSecret] = useState(false);

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
    }
  };

  useEffect(() => {
    fetchData();
  }, [client]);

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
      alert("Error al crear usuario: " + (err.message || err));
    }
  };

  const handleIssueToken = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client) return;
    try {
      const tok = await client.issueToken({
        subject: tokenSubject,
        name: tokenName,
        scopes: ["agent", "observations:read", "observations:write", "graph:read", "graph:write"],
      });
      setIssuedSecret(tok.secret || "Token creado exitosamente");
      fetchData();
    } catch (err: any) {
      alert("Error al emitir token: " + (err.message || err));
    }
  };

  const handleRevokeToken = async (id: string) => {
    if (!confirm("¿Deseas revocar este token permanentemente?")) return;
    if (!client) return;
    try {
      await client.revokeToken(id);
      fetchData();
    } catch (err: any) {
      alert("Error al revocar: " + (err.message || err));
    }
  };

  const openExportModal = (t: Token) => {
    setSelectedTokenForExport(t);
    setIsDownloadModalOpen(true);
  };

  const getExportContext = () => {
    return {
      serverUrl,
      userEmail: selectedTokenForExport?.subject,
      tokenName: selectedTokenForExport?.name,
      projectName: exportProject,
    };
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-white flex items-center gap-2.5">
            <ShieldCheck className="h-6 w-6 text-blue-500" />
            Administración de Usuarios y Agentes
          </h1>
          <p className="text-xs text-slate-400 mt-1">
            Control de identidades, emisión de tokens y descarga de perfiles de configuración para coding agents
          </p>
        </div>

        <div className="flex items-center gap-2.5">
          <Button onClick={() => setIsUserModalOpen(true)} variant="secondary" size="sm" className="gap-2">
            <Users className="h-4 w-4" />
            <span>Crear Usuario</span>
          </Button>
          <Button onClick={() => setIsTokenModalOpen(true)} size="sm" className="gap-2 shadow-lg shadow-blue-600/20">
            <Key className="h-4 w-4" />
            <span>Emitir Token</span>
          </Button>
        </div>
      </div>

      {/* Global Config Download Card */}
      <Card className="p-6 bg-gradient-to-r from-slate-900/90 via-slate-800/80 to-slate-900/90 border-slate-700/80 shadow-2xl">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div className="space-y-1">
            <h2 className="text-base font-semibold text-white flex items-center gap-2">
              <Download className="h-5 w-5 text-blue-400" />
              Descargar Configuraciones para tus Agentes de IA
            </h2>
            <p className="text-xs text-slate-300 max-w-2xl">
              Exporta perfiles para Claude Desktop, Cursor, Windsurf o Cortex CLI. Los archivos nunca incluyen secretos:
              referencian la variable de entorno CORTEX_REMOTE_TOKEN.
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              onClick={() => {
                const content = generateClaudeDesktopConfig(getExportContext());
                downloadFile("claude_desktop_config.json", content);
              }}
              variant="secondary"
              size="sm"
              className="gap-1.5 text-xs"
            >
              <FileCode className="h-3.5 w-3.5 text-blue-400" />
              <span>Claude Desktop</span>
            </Button>

            <Button
              onClick={() => {
                const content = generateCursorMcpConfig(getExportContext());
                downloadFile("cursor_mcp.json", content);
              }}
              variant="secondary"
              size="sm"
              className="gap-1.5 text-xs"
            >
              <FileCode className="h-3.5 w-3.5 text-emerald-400" />
              <span>Cursor MCP</span>
            </Button>

            <Button
              onClick={() => {
                const content = generateCortexYaml(getExportContext());
                downloadFile("cortex.yaml", content, "text/yaml");
              }}
              variant="secondary"
              size="sm"
              className="gap-1.5 text-xs"
            >
              <FileCode className="h-3.5 w-3.5 text-purple-400" />
              <span>cortex.yaml</span>
            </Button>

            <Button
              onClick={() => {
                const content = generateQuickstartScript(getExportContext(), "ps1");
                downloadFile("setup-agent.ps1", content, "text/plain");
              }}
              size="sm"
              className="gap-1.5 text-xs shadow-lg shadow-blue-600/20"
            >
              <Terminal className="h-3.5 w-3.5" />
              <span>PowerShell</span>
            </Button>
          </div>
        </div>
      </Card>

      {/* Two Column Layout for Users and Tokens */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Tokens List */}
        <Card className="p-5 bg-slate-900/70 border-slate-800 flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
              <CardTitle className="text-sm">
                <Key className="h-4 w-4 text-blue-400" />
                Tokens de Autenticación
              </CardTitle>
              <Button onClick={() => setIsTokenModalOpen(true)} variant="ghost" size="sm" className="h-7 text-xs gap-1">
                <Plus className="h-3.5 w-3.5" />
                <span>Nuevo Token</span>
              </Button>
            </div>

            {loading ? (
              <p className="text-xs text-slate-500 py-6 text-center">Cargando tokens...</p>
            ) : tokens.length === 0 ? (
              <p className="text-xs text-slate-500 py-6 text-center">No hay tokens emitidos.</p>
            ) : (
              <div className="space-y-2.5">
                {tokens.map((tok) => (
                  <div
                    key={tok.id}
                    className="p-3.5 bg-slate-950/70 border border-slate-800/80 rounded-lg space-y-2 hover:border-slate-700 transition-colors"
                  >
                    <div className="flex items-center justify-between">
                      <span className="font-semibold text-xs text-slate-200">
                        {tok.name}
                      </span>
                      <Badge variant={tok.revoked_at ? "destructive" : "success"}>
                        {tok.revoked_at ? "Revocado" : "Activo"}
                      </Badge>
                    </div>

                    <div className="flex items-center gap-3 text-[11px] text-slate-500">
                      <span>Prefijo: <code className="text-slate-400 font-mono">{tok.prefix}...</code></span>
                      <span>•</span>
                      <span>Subject: <code className="text-slate-400 font-mono">{tok.subject.slice(0, 8)}...</code></span>
                    </div>

                    <div className="flex items-center gap-2 pt-1">
                      <Button
                        onClick={() => openExportModal(tok)}
                        variant="secondary"
                        size="sm"
                        className="h-7 text-xs gap-1.5"
                      >
                        <Download className="h-3 w-3" />
                        <span>Descargar Config</span>
                      </Button>
                      {!tok.revoked_at && (
                        <Button
                          onClick={() => handleRevokeToken(tok.id)}
                          variant="destructive"
                          size="sm"
                          className="h-7 text-xs gap-1.5"
                        >
                          <Trash2 className="h-3 w-3" />
                          <span>Revocar</span>
                        </Button>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </Card>

        {/* Users List */}
        <Card className="p-5 bg-slate-900/70 border-slate-800 flex flex-col justify-between">
          <div>
            <div className="flex items-center justify-between pb-3 border-b border-slate-800 mb-4">
              <CardTitle className="text-sm">
                <Users className="h-4 w-4 text-purple-400" />
                Usuarios & Agentes Registrados
              </CardTitle>
              <Button onClick={() => setIsUserModalOpen(true)} variant="ghost" size="sm" className="h-7 text-xs gap-1">
                <Plus className="h-3.5 w-3.5" />
                <span>Nuevo Usuario</span>
              </Button>
            </div>

            {loading ? (
              <p className="text-xs text-slate-500 py-6 text-center">Cargando usuarios...</p>
            ) : users.length === 0 ? (
              <p className="text-xs text-slate-500 py-6 text-center">No hay usuarios registrados aún.</p>
            ) : (
              <div className="space-y-2.5">
                {users.map((u) => (
                  <div
                    key={u.id}
                    className="p-3.5 bg-slate-950/70 border border-slate-800/80 rounded-lg flex items-center justify-between hover:border-slate-700 transition-colors"
                  >
                    <div>
                      <div className="font-semibold text-xs text-slate-200">{u.display_name}</div>
                      <div className="text-[11px] text-slate-500">{u.email}</div>
                    </div>

                    <Badge variant="default">
                      {u.roles?.join(", ") || "developer"}
                    </Badge>
                  </div>
                ))}
              </div>
            )}
          </div>
        </Card>
      </div>

      {/* Create User Modal */}
      <Dialog open={isUserModalOpen} onOpenChange={setIsUserModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <Users className="h-4 w-4 text-purple-400" />
            Crear Nuevo Usuario / Agente
          </DialogTitle>
          <DialogClose onClick={() => setIsUserModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleCreateUser} className="space-y-3.5 mt-4 text-xs">
          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              NOMBRE COMPLETO / AGENTE
            </label>
            <Input
              type="text"
              value={userName}
              onChange={(e) => setUserName(e.target.value)}
              placeholder="Ej: Claude Agent 01"
              required
            />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              CORREO ELECTRÓNICO
            </label>
            <Input
              type="email"
              value={userEmail}
              onChange={(e) => setUserEmail(e.target.value)}
              placeholder="agent@cortex.local"
              required
            />
          </div>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              ROL PRINCIPAL
            </label>
            <Select value={userRole} onChange={(e) => setUserRole(e.target.value)}>
              <option value="developer">Developer</option>
              <option value="agent">Autonomous Agent</option>
              <option value="admin">Administrator</option>
            </Select>
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" size="sm" onClick={() => setIsUserModalOpen(false)}>
              Cancelar
            </Button>
            <Button type="submit" size="sm">
              Crear Usuario
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Issue Token Modal */}
      <Dialog open={isTokenModalOpen} onOpenChange={(open) => { setIsTokenModalOpen(open); if (!open) setIssuedSecret(null); }}>
        <DialogHeader>
          <DialogTitle>
            <Key className="h-4 w-4 text-blue-400" />
            Emitir Nuevo Token de Acceso
          </DialogTitle>
          <DialogClose onClick={() => { setIsTokenModalOpen(false); setIssuedSecret(null); }} />
        </DialogHeader>

        {issuedSecret ? (
          <div className="space-y-4 mt-4 text-xs">
            <div className="bg-emerald-500/10 border border-emerald-500/30 text-emerald-400 p-4 rounded-lg space-y-2">
              <span className="font-semibold block">¡Token emitido con éxito!</span>
              <p className="text-[11px] text-slate-300">Copia este secreto ahora; no volverá a mostrarse:</p>
              <div className="p-2.5 bg-slate-950 rounded border border-slate-800 font-mono text-[11px] text-slate-200 break-all select-all">
                {issuedSecret}
              </div>
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => {
                  navigator.clipboard.writeText(issuedSecret);
                  setCopiedSecret(true);
                  setTimeout(() => setCopiedSecret(false), 1800);
                }}
                className="gap-1.5"
              >
                {copiedSecret ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                <span>{copiedSecret ? "Copiado" : "Copiar Secreto"}</span>
              </Button>
              <Button type="button" size="sm" onClick={() => { setIsTokenModalOpen(false); setIssuedSecret(null); }}>
                Listo
              </Button>
            </div>
          </div>
        ) : (
          <form onSubmit={handleIssueToken} className="space-y-3.5 mt-4 text-xs">
            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                USUARIO / SUBJECT ASIGNADO
              </label>
              <Select
                value={tokenSubject}
                onChange={(e) => setTokenSubject(e.target.value)}
                required
              >
                <option value="">Selecciona usuario...</option>
                {users.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name} ({u.email})
                  </option>
                ))}
              </Select>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-slate-300 block uppercase">
                NOMBRE / ETIQUETA DEL TOKEN
              </label>
              <Input
                type="text"
                value={tokenName}
                onChange={(e) => setTokenName(e.target.value)}
                placeholder="Ej: Claude Code Workstation Token"
                required
              />
            </div>

            <div className="flex justify-end gap-2 pt-2">
              <Button type="button" variant="outline" size="sm" onClick={() => setIsTokenModalOpen(false)}>
                Cancelar
              </Button>
              <Button type="submit" size="sm">
                Generar Token
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      {/* Export Config Modal */}
      <Dialog open={isDownloadModalOpen && !!selectedTokenForExport} onOpenChange={setIsDownloadModalOpen}>
        <DialogHeader>
          <DialogTitle>
            <Download className="h-4 w-4 text-blue-400" />
            Descargar Configuración para {selectedTokenForExport?.name}
          </DialogTitle>
          <DialogClose onClick={() => setIsDownloadModalOpen(false)} />
        </DialogHeader>

        <div className="space-y-4 mt-4 text-xs">
          <p className="text-slate-400">
            Selecciona el formato de configuración para {selectedTokenForExport?.name}. Ningún archivo contiene el secreto:
            cada formato indica dónde definir CORTEX_REMOTE_TOKEN.
          </p>

          <div className="space-y-1">
            <label className="text-[11px] font-semibold text-slate-300 block uppercase">
              PROYECTO POR DEFECTO
            </label>
            <Input
              type="text"
              value={exportProject}
              onChange={(e) => setExportProject(e.target.value)}
            />
          </div>

          <div className="grid grid-cols-2 gap-2.5 pt-2">
            <Button
              onClick={() => {
                const c = generateClaudeDesktopConfig(getExportContext());
                downloadFile("claude_desktop_config.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <FileCode className="h-4 w-4 text-blue-400" />
              <span>Claude Desktop</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateCursorMcpConfig(getExportContext());
                downloadFile("cursor_mcp.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <FileCode className="h-4 w-4 text-emerald-400" />
              <span>Cursor IDE</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateWindsurfConfig(getExportContext());
                downloadFile("windsurf_config.json", c);
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <FileCode className="h-4 w-4 text-purple-400" />
              <span>Windsurf Cascade</span>
            </Button>

            <Button
              onClick={() => {
                const c = generateCortexYaml(getExportContext());
                downloadFile("cortex.yaml", c, "text/yaml");
              }}
              variant="secondary"
              size="sm"
              className="justify-start gap-2 h-10"
            >
              <FileCode className="h-4 w-4 text-amber-400" />
              <span>Cortex CLI YAML</span>
            </Button>
          </div>

          <div className="flex justify-end pt-2">
            <Button onClick={() => setIsDownloadModalOpen(false)} size="sm">
              Cerrar
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
