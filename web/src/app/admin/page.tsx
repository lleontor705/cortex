"use client";

import React, { useEffect, useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { User, Token } from "@/lib/api";
import {
  generateClaudeDesktopConfig,
  generateCursorMcpConfig,
  generateWindsurfConfig,
  generateCortexYaml,
  generateEnvFile,
  generateQuickstartScript,
  downloadFile,
} from "@/lib/config-exporter";
import {
  ShieldCheck,
  Key,
  Users,
  Plus,
  Download,
  Copy,
  RefreshCw,
  Trash2,
  CheckCircle,
  FileCode,
  Terminal,
  ExternalLink,
  X,
} from "lucide-react";

export default function AdminPage() {
  const { client, serverUrl, token: activeToken } = useAuth();

  const [users, setUsers] = useState<User[]>([]);
  const [tokens, setTokens] = useState<Token[]>([]);
  const [loading, setLoading] = useState(true);

  // New User Modal
  const [isUserModalOpen, setIsUserModalOpen] = useState(false);
  const [userEmail, setUserEmail] = useState("");
  const [userName, setUserName] = useState("");
  const [userRole, setUserRole] = useState("developer");

  // New Token Modal
  const [isTokenModalOpen, setIsTokenModalOpen] = useState(false);
  const [tokenSubject, setTokenSubject] = useState("");
  const [tokenName, setTokenName] = useState("");
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);

  // Download Config Modal
  const [isDownloadModalOpen, setIsDownloadModalOpen] = useState(false);
  const [selectedTokenForExport, setSelectedTokenForExport] = useState<Token | null>(null);
  const [exportProject, setExportProject] = useState("default");

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
      token: (selectedTokenForExport?.secret || activeToken || ""),
      userEmail: selectedTokenForExport?.subject,
      tokenName: selectedTokenForExport?.name,
      projectName: exportProject,
    };
  };

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "24px", flexWrap: "wrap", gap: "16px" }}>
        <div>
          <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
            Administración de Usuarios y Agentes
          </h1>
          <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
            Control de identidades, emisión de tokens y descarga de perfiles de configuración para coding agents
          </p>
        </div>

        <div style={{ display: "flex", gap: "10px" }}>
          <button onClick={() => setIsUserModalOpen(true)} className="btn btn-secondary">
            <Users size={15} />
            <span>Crear Usuario</span>
          </button>
          <button onClick={() => setIsTokenModalOpen(true)} className="btn btn-primary">
            <Key size={15} />
            <span>Emitir Token</span>
          </button>
        </div>
      </div>

      {/* Global Config Download Card */}
      <div
        className="card"
        style={{
          marginBottom: "28px",
          background: "linear-gradient(135deg, rgba(30, 41, 59, 0.9) 0%, rgba(15, 23, 42, 0.9) 100%)",
          border: "1px solid var(--border-default)",
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", flexWrap: "wrap", gap: "16px" }}>
          <div>
            <h2 style={{ fontSize: "16px", fontWeight: "600", marginBottom: "4px", display: "flex", alignItems: "center", gap: "8px" }}>
              <Download size={18} color="var(--accent-primary)" />
              Descargar Configuraciones para tus Agentes de IA
            </h2>
            <p style={{ color: "var(--text-secondary)", fontSize: "13px" }}>
              Exporta archivos de configuración preconfigurados con tu servidor y tokens para Claude Desktop, Cursor, Windsurf o Cortex CLI.
            </p>
          </div>

          <div style={{ display: "flex", gap: "8px", flexWrap: "wrap" }}>
            <button
              onClick={() => {
                const content = generateClaudeDesktopConfig(getExportContext());
                downloadFile("claude_desktop_config.json", content);
              }}
              className="btn btn-secondary btn-sm"
            >
              <FileCode size={13} />
              <span>Claude Desktop JSON</span>
            </button>

            <button
              onClick={() => {
                const content = generateCursorMcpConfig(getExportContext());
                downloadFile("cursor_mcp.json", content);
              }}
              className="btn btn-secondary btn-sm"
            >
              <FileCode size={13} />
              <span>Cursor MCP JSON</span>
            </button>

            <button
              onClick={() => {
                const content = generateCortexYaml(getExportContext());
                downloadFile("cortex.yaml", content, "text/yaml");
              }}
              className="btn btn-secondary btn-sm"
            >
              <FileCode size={13} />
              <span>cortex.yaml</span>
            </button>

            <button
              onClick={() => {
                const content = generateQuickstartScript(getExportContext(), "ps1");
                downloadFile("setup-agent.ps1", content, "text/plain");
              }}
              className="btn btn-primary btn-sm"
            >
              <Terminal size={13} />
              <span>Script PowerShell</span>
            </button>
          </div>
        </div>
      </div>

      {/* Two Column Layout for Users and Tokens */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(420px, 1fr))", gap: "24px" }}>
        {/* Tokens List */}
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <Key size={17} />
              Tokens de Autenticación
            </h2>
            <button onClick={() => setIsTokenModalOpen(true)} className="btn btn-secondary btn-sm">
              <Plus size={13} />
              <span>Nuevo Token</span>
            </button>
          </div>

          {loading ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>Cargando tokens...</p>
          ) : tokens.length === 0 ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>No hay tokens emitidos.</p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
              {tokens.map((tok) => (
                <div
                  key={tok.id}
                  style={{
                    padding: "12px",
                    backgroundColor: "var(--bg-input)",
                    border: "1px solid var(--border-subtle)",
                    borderRadius: "var(--radius-md)",
                    display: "flex",
                    flexDirection: "column",
                    gap: "8px",
                  }}
                >
                  <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
                    <span style={{ fontWeight: "600", fontSize: "13px", color: "var(--text-primary)" }}>
                      {tok.name}
                    </span>
                    <span className={`badge ${tok.revoked_at ? "badge-red" : "badge-green"}`}>
                      {tok.revoked_at ? "Revocado" : "Activo"}
                    </span>
                  </div>

                  <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>
                    <span>Prefijo: <code className="font-mono">{tok.prefix}...</code></span>
                    <span style={{ margin: "0 6px" }}>•</span>
                    <span>Subject: <code className="font-mono">{tok.subject.slice(0, 8)}...</code></span>
                  </div>

                  <div style={{ display: "flex", gap: "8px", marginTop: "4px" }}>
                    <button
                      onClick={() => openExportModal(tok)}
                      className="btn btn-secondary btn-sm"
                      style={{ fontSize: "11px", padding: "4px 8px" }}
                    >
                      <Download size={12} />
                      <span>Descargar Config</span>
                    </button>
                    {!tok.revoked_at && (
                      <button
                        onClick={() => handleRevokeToken(tok.id)}
                        className="btn btn-danger btn-sm"
                        style={{ fontSize: "11px", padding: "4px 8px" }}
                      >
                        <Trash2 size={12} />
                        <span>Revocar</span>
                      </button>
                    )}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>

        {/* Users List */}
        <div className="card">
          <div className="card-header">
            <h2 className="card-title">
              <Users size={17} />
              Usuarios & Agentes Registrados
            </h2>
            <button onClick={() => setIsUserModalOpen(true)} className="btn btn-secondary btn-sm">
              <Plus size={13} />
              <span>Nuevo Usuario</span>
            </button>
          </div>

          {loading ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>Cargando usuarios...</p>
          ) : users.length === 0 ? (
            <p style={{ color: "var(--text-muted)", fontSize: "13px" }}>No hay usuarios registrados aún.</p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: "10px" }}>
              {users.map((u) => (
                <div
                  key={u.id}
                  style={{
                    padding: "12px",
                    backgroundColor: "var(--bg-input)",
                    border: "1px solid var(--border-subtle)",
                    borderRadius: "var(--radius-md)",
                    display: "flex",
                    justifyContent: "space-between",
                    alignItems: "center",
                  }}
                >
                  <div>
                    <div style={{ fontWeight: "600", fontSize: "13px" }}>{u.display_name}</div>
                    <div style={{ fontSize: "11px", color: "var(--text-muted)" }}>{u.email}</div>
                  </div>

                  <div style={{ display: "flex", alignItems: "center", gap: "8px" }}>
                    <span className="badge badge-blue">
                      {u.roles?.join(", ") || "developer"}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Create User Modal */}
      {isUserModalOpen && (
        <div style={{ position: "fixed", inset: 0, backgroundColor: "rgba(0,0,0,0.7)", backdropFilter: "blur(4px)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: "20px" }}>
          <div className="card" style={{ maxWidth: "480px", width: "100%" }}>
            <div className="card-header">
              <h2 className="card-title">
                <Users size={18} />
                Crear Nuevo Usuario
              </h2>
              <button onClick={() => setIsUserModalOpen(false)} className="btn btn-secondary btn-sm" style={{ padding: "4px" }}>
                <X size={16} />
              </button>
            </div>

            <form onSubmit={handleCreateUser} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  NOMBRE COMPLETO / AGENTE
                </label>
                <input
                  type="text"
                  className="input"
                  value={userName}
                  onChange={(e) => setUserName(e.target.value)}
                  placeholder="Ej: Claude Agent 01"
                  required
                />
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  CORREO ELECTRÓNICO
                </label>
                <input
                  type="email"
                  className="input"
                  value={userEmail}
                  onChange={(e) => setUserEmail(e.target.value)}
                  placeholder="agent@cortex.local"
                  required
                />
              </div>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  ROL PRINCIPAL
                </label>
                <select className="select" value={userRole} onChange={(e) => setUserRole(e.target.value)}>
                  <option value="developer">Developer</option>
                  <option value="agent">Autonomous Agent</option>
                  <option value="admin">Administrator</option>
                </select>
              </div>

              <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "8px" }}>
                <button type="button" onClick={() => setIsUserModalOpen(false)} className="btn btn-secondary">
                  Cancelar
                </button>
                <button type="submit" className="btn btn-primary">
                  Crear Usuario
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Issue Token Modal */}
      {isTokenModalOpen && (
        <div style={{ position: "fixed", inset: 0, backgroundColor: "rgba(0,0,0,0.7)", backdropFilter: "blur(4px)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: "20px" }}>
          <div className="card" style={{ maxWidth: "480px", width: "100%" }}>
            <div className="card-header">
              <h2 className="card-title">
                <Key size={18} />
                Emitir Nuevo Token de Acceso
              </h2>
              <button onClick={() => { setIsTokenModalOpen(false); setIssuedSecret(null); }} className="btn btn-secondary btn-sm" style={{ padding: "4px" }}>
                <X size={16} />
              </button>
            </div>

            {issuedSecret ? (
              <div>
                <div style={{ backgroundColor: "var(--success-bg)", border: "1px solid rgba(16, 185, 129, 0.3)", color: "var(--success)", padding: "14px", borderRadius: "var(--radius-md)", fontSize: "13px", marginBottom: "16px" }}>
                  <b>¡Token emitido con éxito!</b> Copia este secreto ahora; no volverá a mostrarse:
                  <div className="font-mono" style={{ marginTop: "8px", padding: "8px", background: "var(--bg-input)", borderRadius: "4px", color: "var(--text-primary)", wordBreak: "break-all" }}>
                    {issuedSecret}
                  </div>
                </div>

                <div style={{ display: "flex", gap: "10px", justifyContent: "flex-end" }}>
                  <button
                    onClick={() => {
                      navigator.clipboard.writeText(issuedSecret);
                      alert("¡Token copiado al portapapeles!");
                    }}
                    className="btn btn-secondary"
                  >
                    <Copy size={14} />
                    <span>Copiar Secreto</span>
                  </button>
                  <button onClick={() => { setIsTokenModalOpen(false); setIssuedSecret(null); }} className="btn btn-primary">
                    Listo
                  </button>
                </div>
              </div>
            ) : (
              <form onSubmit={handleIssueToken} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                    USUARIO / SUBJECT ASIGNADO
                  </label>
                  <select
                    className="select"
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
                  </select>
                </div>

                <div>
                  <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                    NOMBRE / ETIQUETA DEL TOKEN
                  </label>
                  <input
                    type="text"
                    className="input"
                    value={tokenName}
                    onChange={(e) => setTokenName(e.target.value)}
                    placeholder="Ej: Claude Code Workstation Token"
                    required
                  />
                </div>

                <div style={{ display: "flex", justifyContent: "flex-end", gap: "10px", marginTop: "8px" }}>
                  <button type="button" onClick={() => setIsTokenModalOpen(false)} className="btn btn-secondary">
                    Cancelar
                  </button>
                  <button type="submit" className="btn btn-primary">
                    Generar Token
                  </button>
                </div>
              </form>
            )}
          </div>
        </div>
      )}

      {/* Export Config Modal */}
      {isDownloadModalOpen && selectedTokenForExport && (
        <div style={{ position: "fixed", inset: 0, backgroundColor: "rgba(0,0,0,0.7)", backdropFilter: "blur(4px)", display: "flex", alignItems: "center", justifyContent: "center", zIndex: 50, padding: "20px" }}>
          <div className="card" style={{ maxWidth: "540px", width: "100%" }}>
            <div className="card-header">
              <h2 className="card-title">
                <Download size={18} />
                Descargar Configuración para {selectedTokenForExport.name}
              </h2>
              <button onClick={() => setIsDownloadModalOpen(false)} className="btn btn-secondary btn-sm" style={{ padding: "4px" }}>
                <X size={16} />
              </button>
            </div>

            <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
              <p style={{ fontSize: "13px", color: "var(--text-secondary)" }}>
                Selecciona el formato de configuración que deseas descargar para este token:
              </p>

              <div>
                <label style={{ display: "block", fontSize: "12px", fontWeight: "600", color: "var(--text-secondary)", marginBottom: "4px" }}>
                  PROYECTO POR DEFECTO
                </label>
                <input
                  type="text"
                  className="input"
                  value={exportProject}
                  onChange={(e) => setExportProject(e.target.value)}
                />
              </div>

              <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "10px", marginTop: "8px" }}>
                <button
                  onClick={() => {
                    const c = generateClaudeDesktopConfig(getExportContext());
                    downloadFile("claude_desktop_config.json", c);
                  }}
                  className="btn btn-secondary"
                  style={{ justifyContent: "flex-start", padding: "12px" }}
                >
                  <FileCode size={15} color="var(--accent-primary)" />
                  <span>Claude Desktop</span>
                </button>

                <button
                  onClick={() => {
                    const c = generateCursorMcpConfig(getExportContext());
                    downloadFile("cursor_mcp.json", c);
                  }}
                  className="btn btn-secondary"
                  style={{ justifyContent: "flex-start", padding: "12px" }}
                >
                  <FileCode size={15} color="#10b981" />
                  <span>Cursor IDE</span>
                </button>

                <button
                  onClick={() => {
                    const c = generateWindsurfConfig(getExportContext());
                    downloadFile("windsurf_config.json", c);
                  }}
                  className="btn btn-secondary"
                  style={{ justifyContent: "flex-start", padding: "12px" }}
                >
                  <FileCode size={15} color="#8b5cf6" />
                  <span>Windsurf Cascade</span>
                </button>

                <button
                  onClick={() => {
                    const c = generateCortexYaml(getExportContext());
                    downloadFile("cortex.yaml", c, "text/yaml");
                  }}
                  className="btn btn-secondary"
                  style={{ justifyContent: "flex-start", padding: "12px" }}
                >
                  <FileCode size={15} color="#f59e0b" />
                  <span>Cortex CLI YAML</span>
                </button>
              </div>

              <div style={{ display: "flex", justifyContent: "flex-end", marginTop: "12px" }}>
                <button onClick={() => setIsDownloadModalOpen(false)} className="btn btn-primary">
                  Cerrar
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
