"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  ProjectArtifactItem,
  ProjectContext,
  SaveProjectArtifactInput,
} from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import {
  Dialog,
  DialogHeader,
  DialogTitle,
  DialogClose,
} from "@/components/ui/dialog";
import {
  FolderKanban,
  Sparkles,
  Bot,
  Plus,
  Trash2,
  Edit3,
  CheckCircle2,
  Code2,
  BookOpen,
  Copy,
  Check,
  RefreshCw,
  Terminal,
  ShieldCheck,
  Layers,
  Wand2,
  ChevronRight,
  FileCode,
  Sliders,
  Share2,
  Eye,
  Info,
  Lock,
} from "lucide-react";

export default function ProjectsPage() {
  const router = useRouter();
  const { client, principal, llmApiKey, llmProvider, llmModel, llmBaseURL } = useAuth();
  const [projects, setProjects] = useState<string[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>("");
  const [activeTab, setActiveTab] = useState<"rules" | "skills" | "simulator" | "ai_assistant">(
    "rules",
  );

  const [artifacts, setArtifacts] = useState<ProjectArtifactItem[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [projectContext, setProjectContext] = useState<ProjectContext | null>(
    null,
  );
  const [contextLoading, setContextLoading] = useState<boolean>(false);

  // Modal State
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [editingArtifact, setEditingArtifact] =
    useState<ProjectArtifactItem | null>(null);
  const [viewingArtifact, setViewingArtifact] =
    useState<ProjectArtifactItem | null>(null);
  const [modalKind, setModalKind] = useState<"rule" | "skill">("rule");
  const [modalKey, setModalKey] = useState("");
  const [modalTitle, setModalTitle] = useState("");
  const [modalDesc, setModalDesc] = useState("");
  const [modalContent, setModalContent] = useState("");
  const [modalScope, setModalScope] = useState<
    "project" | "workspace_default"
  >("project");
  const [saving, setSaving] = useState(false);
  const [copiedKey, setCopiedKey] = useState<string | null>(null);
  const [projectSyncEnabled, setProjectSyncEnabled] = useState<boolean>(true);

  // AI Assistant State
  const [aiPrompt, setAiPrompt] = useState("");
  const [aiTargetKind, setAiTargetKind] = useState<"rule" | "skill">("rule");
  const [isGeneratingAi, setIsGeneratingAi] = useState(false);

  const userRoles = principal?.roles || ["developer"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );

  useEffect(() => {
    loadProjects();
  }, [client]);

  useEffect(() => {
    loadArtifacts();
  }, [client, selectedProject]);

  const loadProjects = async () => {
    if (!client) return;
    try {
      const p = await client.projects();
      setProjects(p || []);
      if (!selectedProject && p && p.length > 0) {
        setSelectedProject(p[0]);
      }
    } catch {
      setProjects(["default", "cortex-core", "api-service"]);
    }
  };

  const loadArtifacts = async () => {
    if (!client) return;
    setLoading(true);
    try {
      const items = await client.listProjectArtifacts(selectedProject);
      setArtifacts(items || []);
    } catch {
      setArtifacts([
        {
          id: "rule-1",
          kind: "rule",
          key: "architecture_clean",
          title: "Clean Architecture & Zero CGO",
          description: "Governance rule for domain boundaries",
          content:
            "- All business models must reside in internal/domain.\n- Maintain Zero CGO gate on local SQLite runtime.\n- Use dependency bundles at composition roots.",
          scope: "workspace_default",
          revision: 1,
          status: "active",
          updated_at: new Date().toISOString(),
        },
        {
          id: "skill-1",
          kind: "skill",
          key: "run_ci_suite",
          title: "Run Full CI Test Suite",
          description: "Executes linting, unit tests, and offline gate",
          content:
            "1. Run `make fmt`\n2. Run `golangci-lint run ./...`\n3. Run `go test -v -count=1 ./...`\n4. Report results to operator.",
          scope: "workspace_default",
          revision: 1,
          status: "active",
          updated_at: new Date().toISOString(),
        },
      ]);
    } finally {
      setLoading(false);
    }
  };

  const loadContextSimulator = async () => {
    if (!client) return;
    setContextLoading(true);
    try {
      const ctx = await client.getProjectContext(selectedProject);
      setProjectContext(ctx);
    } catch {
      setProjectContext({
        project: selectedProject || "workspace_default",
        system_prompt: `# Corporate & Project Directives for [${selectedProject || "workspace"}]\n\n## Rule: Clean Architecture & Zero CGO\n- All business models must reside in internal/domain.\n- Maintain Zero CGO gate on local SQLite runtime.\n\nStandard enterprise development governance and security guidelines apply.`,
        rules: [
          {
            key: "architecture_clean",
            title: "Clean Architecture & Zero CGO",
            content: "Maintain Zero CGO gate on local SQLite runtime.",
            scope: "workspace_default",
          },
        ],
        skills: [
          {
            key: "run_ci_suite",
            title: "Run Full CI Test Suite",
            description: "Executes linting, unit tests, and offline gate",
            scope: "workspace_default",
            project: selectedProject,
          },
        ],
      });
    } finally {
      setContextLoading(false);
    }
  };

  const openCreateModal = (kind: "rule" | "skill") => {
    setEditingArtifact(null);
    setModalKind(kind);
    setModalKey("");
    setModalTitle("");
    setModalDesc("");
    setModalContent("");
    setModalScope(selectedProject ? "project" : "workspace_default");
    setIsModalOpen(true);
  };

  const openEditModal = (item: ProjectArtifactItem) => {
    setEditingArtifact(item);
    setModalKind(item.kind);
    setModalKey(item.key);
    setModalTitle(item.title);
    setModalDesc(item.description || "");
    setModalContent(item.content);
    setModalScope(item.scope);
    setIsModalOpen(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!modalKey || !modalTitle || !modalContent || !client) return;

    setSaving(true);
    try {
      const input: SaveProjectArtifactInput = {
        kind: modalKind,
        key: modalKey,
        title: modalTitle,
        description: modalDesc,
        content: modalContent,
        scope: modalScope,
        project: modalScope === "project" ? selectedProject : undefined,
      };
      await client.saveProjectArtifact(input);
      setIsModalOpen(false);
      await loadArtifacts();
      if (activeTab === "simulator") {
        await loadContextSimulator();
      }
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Error al guardar");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (id: string) => {
    if (!client) return;
    if (!confirm("¿Deseas eliminar este artefacto corporativo?")) return;
    try {
      await client.deleteProjectArtifact(id);
      await loadArtifacts();
      if (activeTab === "simulator") {
        await loadContextSimulator();
      }
    } catch (err: unknown) {
      alert(err instanceof Error ? err.message : "Error al eliminar");
    }
  };

  const copyToClipboard = (text: string, key: string) => {
    navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(null), 2000);
  };

  const handleGenerateAiArtifact = async () => {
    if (!aiPrompt.trim()) return;
    setIsGeneratingAi(true);

    try {
      // Create quick structured guideline from prompt
      const key = aiPrompt
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "_")
        .slice(0, 24);
      const title = aiPrompt.charAt(0).toUpperCase() + aiPrompt.slice(1);
      const content = `## Directiva: ${title}\n\n- ${aiPrompt}\n- Aplicar validaciones estrictas y verificación de tipos.\n- Mantener compatibilidad con arquitectura Zero-CGO de Cortex.`;

      setModalKind(aiTargetKind);
      setModalKey(key);
      setModalTitle(title);
      setModalDesc(`Generado con asistencia de IA para ${selectedProject || "Workspace"}`);
      setModalContent(content);
      setModalScope(selectedProject ? "project" : "workspace_default");
      setIsModalOpen(true);
      setAiPrompt("");
    } finally {
      setIsGeneratingAi(false);
    }
  };

  const rulesList = artifacts.filter((a) => a.kind === "rule");
  const skillsList = artifacts.filter((a) => a.kind === "skill");

  return (
    <div className="space-y-6">
      {/* Redesigned Hero Control Header */}
      <div className="p-5 sm:p-7 rounded-2xl bg-gradient-to-r from-blue-950/40 via-[var(--bg-secondary)] to-indigo-950/30 border border-[var(--border-subtle)] shadow-2xl relative overflow-hidden">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-5 relative z-10">
          <div className="space-y-1.5 max-w-2xl">
            <div className="flex items-center gap-2">
              <span className="w-8 h-8 rounded-xl bg-blue-500/20 text-blue-400 border border-blue-500/30 flex items-center justify-center font-bold text-sm">
                <FolderKanban className="h-4 w-4" />
              </span>
              <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-[var(--text-primary)]">
                Proyectos, Directivas & Skills MCP
              </h1>
              <Badge
                variant="outline"
                className={`text-[10px] px-2 font-mono uppercase ${
                  isAdmin
                    ? "border-purple-500/40 text-purple-400 bg-purple-500/10"
                    : "border-blue-500/40 text-blue-400 bg-blue-500/10"
                }`}
              >
                {isAdmin ? "⚡ GOBERNANZA & EDICIÓN" : "👤 MODO CONSULTA MCP"}
              </Badge>
            </div>
            <p className="text-xs sm:text-sm text-[var(--text-secondary)] leading-relaxed">
              {isAdmin
                ? "Gobierno centralizado de System Prompts, arquitectura limpia y catálogo de herramientas corporativas inyectadas en tiempo de ejecución a Claude, Cursor y Windsurf."
                : "Catálogo de directivas corporativas y herramientas de procedimiento inyectadas automáticamente en tiempo de ejecución a tus Coding Agents vía MCP."}
            </p>
          </div>

          {/* Quick Stats Bar */}
          <div className="flex flex-wrap items-center gap-3 shrink-0">
            <div className="p-3 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-center gap-3">
              <div className="text-left">
                <div className="text-[10px] text-[var(--text-muted)] uppercase tracking-wider font-semibold">Reglas Activas</div>
                <div className="text-lg font-bold text-blue-400">{rulesList.length}</div>
              </div>
              <ShieldCheck className="h-5 w-5 text-blue-500/40" />
            </div>

            <div className="p-3 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-center gap-3">
              <div className="text-left">
                <div className="text-[10px] text-[var(--text-muted)] uppercase tracking-wider font-semibold">Skills MCP</div>
                <div className="text-lg font-bold text-amber-400">{skillsList.length}</div>
              </div>
              <Sparkles className="h-5 w-5 text-amber-500/40" />
            </div>
          </div>
        </div>

        {/* Project Selector & Actions Bar */}
        <div className="mt-5 pt-4 border-t border-[var(--border-subtle)] flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2.5">
            <div className="flex items-center gap-2 bg-[var(--bg-surface)] border border-[var(--border-subtle)] px-3 py-1 rounded-xl shadow-sm">
              <Layers className="h-4 w-4 text-blue-400 shrink-0" />
              <Select
                value={selectedProject}
                onChange={(e) => setSelectedProject(e.target.value)}
                className="bg-transparent border-0 font-semibold text-xs text-[var(--text-primary)] focus:ring-0 cursor-pointer min-w-[200px]"
              >
                <option value="">🌐 Corporativo Global (Workspace)</option>
                {projects.map((p) => (
                  <option key={p} value={p}>
                    📁 Proyecto: {p}
                  </option>
                ))}
              </Select>
            </div>

            <Button
              variant="default"
              size="sm"
              onClick={() => router.push(`/graph?project=${encodeURIComponent(selectedProject || "all")}`)}
              className="text-xs gap-1.5 bg-blue-600 hover:bg-blue-500 text-white shadow-md shadow-blue-600/20"
              title="Explorar el Grafo Completo del Proyecto en Cortex Web"
            >
              <Share2 className="h-3.5 w-3.5" />
              <span>Ver Grafo del Proyecto</span>
            </Button>

            <button
              type="button"
              onClick={() => setProjectSyncEnabled(!projectSyncEnabled)}
              className={`px-3 py-1.5 rounded-xl text-xs font-semibold flex items-center gap-1.5 transition-all ${
                projectSyncEnabled
                  ? "bg-blue-600/20 text-blue-400 border border-blue-500/30"
                  : "bg-[var(--bg-surface)] text-[var(--text-muted)] border border-[var(--border-subtle)]"
              }`}
              title="Alternar sincronización a Cortex Server"
            >
              {projectSyncEnabled ? "☁️ Cloud Sync: ON" : "🔒 Local Only"}
            </button>
          </div>

          {isAdmin && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => {
                const np = prompt("Nombre del nuevo proyecto:");
                if (np && np.trim()) {
                  const name = np.trim().toLowerCase().replace(/\s+/g, "-");
                  setProjects((prev) => Array.from(new Set([...prev, name])));
                  setSelectedProject(name);
                }
              }}
              className="text-xs gap-1.5 border-[var(--border-subtle)] bg-[var(--bg-surface)]"
            >
              <Plus className="h-3.5 w-3.5" />
              <span>Nuevo Proyecto</span>
            </Button>
          )}
        </div>
      </div>

      {/* Modern Navigation Tabs */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-[var(--border-subtle)] pb-2">
        <div className="flex items-center gap-2 overflow-x-auto pb-1 sm:pb-0">
          <button
            type="button"
            onClick={() => setActiveTab("rules")}
            className={`px-3.5 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
              activeTab === "rules"
                ? "bg-[var(--accent-primary)] text-white shadow-lg shadow-blue-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <ShieldCheck className="h-4 w-4" />
            <span>Directivas & Reglas ({rulesList.length})</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("skills")}
            className={`px-3.5 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
              activeTab === "skills"
                ? "bg-amber-600 text-white shadow-lg shadow-amber-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <Sparkles className="h-4 w-4" />
            <span>Catálogo de Skills ({skillsList.length})</span>
          </button>

          <button
            type="button"
            onClick={() => {
              setActiveTab("simulator");
              loadContextSimulator();
            }}
            className={`px-3.5 py-2 rounded-xl text-xs font-semibold font-mono flex items-center gap-2 transition-all ${
              activeTab === "simulator"
                ? "bg-emerald-600 text-white shadow-lg shadow-emerald-600/20"
                : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
            }`}
          >
            <Terminal className="h-4 w-4" />
            <span>Simulador MCP</span>
          </button>

          {isAdmin && (
            <button
              type="button"
              onClick={() => setActiveTab("ai_assistant")}
              className={`px-3.5 py-2 rounded-xl text-xs font-semibold flex items-center gap-2 transition-all ${
                activeTab === "ai_assistant"
                  ? "bg-purple-600 text-white shadow-lg shadow-purple-600/20"
                  : "bg-[var(--bg-surface)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] border border-[var(--border-subtle)]"
              }`}
            >
              <Wand2 className="h-4 w-4 text-purple-300" />
              <span>Generador IA (Admin)</span>
            </button>
          )}
        </div>

        {isAdmin && activeTab !== "simulator" && activeTab !== "ai_assistant" && (
          <Button
            variant="default"
            size="sm"
            onClick={() =>
              openCreateModal(activeTab === "rules" ? "rule" : "skill")
            }
            className="gap-1.5 text-xs shadow-md shadow-blue-500/20 shrink-0"
          >
            <Plus className="h-4 w-4" />
            <span>{activeTab === "rules" ? "Nueva Regla" : "Nuevo Skill"}</span>
          </Button>
        )}
      </div>

      {/* Tab 1: Rules & System Prompts */}
      {activeTab === "rules" && (
        <div className="space-y-4">
          <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-start gap-3">
            <ShieldCheck className="h-5 w-5 text-blue-400 shrink-0 mt-0.5" />
            <div>
              <h4 className="text-xs sm:text-sm font-semibold text-[var(--text-primary)]">
                Jerarquía Dinámica de System Prompts
              </h4>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                Las directivas de alcance <b className="text-blue-400">Global Workspace</b> aplican a todos los agentes. Al consultar <code className="font-mono text-[11px] bg-[var(--bg-secondary)] px-1 py-0.5 rounded">cortex_get_project_context</code>, se agregan y combinan con las directivas específicas del proyecto activo.
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-[var(--text-muted)] text-sm">
              Cargando reglas y directivas...
            </div>
          ) : rulesList.length === 0 ? (
            <Card className="border-dashed border-2 border-[var(--border-subtle)] p-8 text-center bg-[var(--bg-secondary)]">
              <BookOpen className="h-10 w-10 text-[var(--text-muted)] mx-auto mb-3" />
              <h3 className="text-sm font-medium text-[var(--text-primary)]">Sin directivas registradas</h3>
              <p className="text-xs text-[var(--text-muted)] mt-1 max-w-sm mx-auto">
                {isAdmin
                  ? "Crea reglas de Clean Architecture, Zero CGO, o directrices de seguridad para este proyecto."
                  : "No hay directivas asignadas a este proyecto. Consulta con el Administrador para crear reglas corporativas."}
              </p>
              {isAdmin && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => openCreateModal("rule")}
                  className="mt-4 text-xs"
                >
                  <Plus className="h-3.5 w-3.5 mr-1" /> Crear Primera Directiva
                </Button>
              )}
            </Card>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {rulesList.map((rule) => (
                <Card
                  key={rule.id}
                  className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] hover:border-blue-500/50 transition-all flex flex-col justify-between shadow-xl"
                >
                  <CardHeader className="p-4 pb-2 border-b border-[var(--border-subtle)]">
                    <div className="flex items-start justify-between gap-2">
                      <div className="overflow-hidden">
                        <div className="flex items-center gap-1.5">
                          <CardTitle className="text-sm font-semibold text-[var(--text-primary)] truncate">
                            {rule.title}
                          </CardTitle>
                        </div>
                        <div className="flex items-center gap-2 mt-1">
                          <Badge
                            variant={rule.scope === "workspace_default" ? "outline" : "secondary"}
                            className="text-[9px] px-1.5 py-0 font-mono"
                          >
                            {rule.scope === "workspace_default" ? "Global" : "Proyecto"}
                          </Badge>
                          <span className="text-[10px] font-mono text-[var(--text-muted)]">
                            key: {rule.key}
                          </span>
                        </div>
                      </div>

                      <div className="flex items-center gap-1 shrink-0">
                        <button
                          type="button"
                          onClick={() => setViewingArtifact(rule)}
                          className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-blue-400 transition-colors"
                          title="Ver detalle de directiva"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(rule.content, rule.id)}
                          className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-white transition-colors"
                          title="Copiar prompt"
                        >
                          {copiedKey === rule.id ? (
                            <Check className="h-3.5 w-3.5 text-emerald-400" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                        {isAdmin && (
                          <>
                            <button
                              type="button"
                              onClick={() => openEditModal(rule)}
                              className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-blue-400 transition-colors"
                              title="Editar"
                            >
                              <Edit3 className="h-3.5 w-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDelete(rule.id)}
                              className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-rose-400 transition-colors"
                              title="Eliminar"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  </CardHeader>

                  <CardContent className="p-4 pt-3 space-y-2.5">
                    {rule.description && (
                      <p className="text-xs text-[var(--text-secondary)] line-clamp-2">
                        {rule.description}
                      </p>
                    )}
                    <pre className="bg-[var(--bg-surface)] rounded-xl p-3 font-mono text-xs text-[var(--text-primary)] whitespace-pre-wrap max-h-36 overflow-y-auto border border-[var(--border-subtle)]">
                      {rule.content}
                    </pre>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Tab 2: Skills Catalog */}
      {activeTab === "skills" && (
        <div className="space-y-4">
          <div className="p-4 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] flex items-start gap-3">
            <Sparkles className="h-5 w-5 text-amber-400 shrink-0 mt-0.5" />
            <div>
              <h4 className="text-xs sm:text-sm font-semibold text-[var(--text-primary)]">
                Catálogo de Herramientas Corporativas MCP
              </h4>
              <p className="text-xs text-[var(--text-secondary)] mt-0.5">
                Los skills son procedimientos reutilizables que los agentes descubren vía <code className="font-mono text-[11px] bg-[var(--bg-secondary)] px-1 py-0.5 rounded">cortex_list_skills</code> e invocan con <code className="font-mono text-[11px] bg-[var(--bg-secondary)] px-1 py-0.5 rounded">cortex_get_skill</code>.
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-[var(--text-muted)] text-sm">
              Cargando catálogo de skills...
            </div>
          ) : skillsList.length === 0 ? (
            <Card className="border-dashed border-2 border-[var(--border-subtle)] p-8 text-center bg-[var(--bg-secondary)]">
              <Code2 className="h-10 w-10 text-[var(--text-muted)] mx-auto mb-3" />
              <h3 className="text-sm font-medium text-[var(--text-primary)]">Sin skills registrados</h3>
              <p className="text-xs text-[var(--text-muted)] mt-1 max-w-sm mx-auto">
                {isAdmin
                  ? "Crea habilidades corporativas (despliegues, linters, migraciones) accesibles por agentes AI."
                  : "No hay skills registrados para este proyecto. El Administrador puede añadir herramientas al catálogo MCP."}
              </p>
              {isAdmin && (
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => openCreateModal("skill")}
                  className="mt-4 text-xs"
                >
                  <Plus className="h-3.5 w-3.5 mr-1" /> Registrar Primer Skill
                </Button>
              )}
            </Card>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {skillsList.map((skill) => (
                <Card
                  key={skill.id}
                  className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] hover:border-amber-500/50 transition-all flex flex-col justify-between shadow-xl"
                >
                  <CardHeader className="p-4 pb-2 border-b border-[var(--border-subtle)]">
                    <div className="flex items-start justify-between gap-2">
                      <div className="overflow-hidden">
                        <div className="flex items-center gap-1.5">
                          <CardTitle className="text-sm font-semibold text-[var(--text-primary)] truncate">
                            {skill.title}
                          </CardTitle>
                        </div>
                        <div className="flex items-center gap-2 mt-1">
                          <Badge variant="secondary" className="text-[9px] px-1.5 py-0 font-mono text-amber-400">
                            MCP Tool
                          </Badge>
                          <span className="text-[10px] font-mono text-[var(--text-muted)] truncate">
                            key: {skill.key}
                          </span>
                        </div>
                      </div>

                      <div className="flex items-center gap-1 shrink-0">
                        <button
                          type="button"
                          onClick={() => setViewingArtifact(skill)}
                          className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-amber-400 transition-colors"
                          title="Ver detalle del skill"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(skill.content, skill.id)}
                          className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-white transition-colors"
                          title="Copiar instrucciones"
                        >
                          {copiedKey === skill.id ? (
                            <Check className="h-3.5 w-3.5 text-emerald-400" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                        {isAdmin && (
                          <>
                            <button
                              type="button"
                              onClick={() => openEditModal(skill)}
                              className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-amber-400 transition-colors"
                              title="Editar"
                            >
                              <Edit3 className="h-3.5 w-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDelete(skill.id)}
                              className="p-1.5 rounded-lg text-[var(--text-muted)] hover:text-rose-400 transition-colors"
                              title="Eliminar"
                            >
                              <Trash2 className="h-3.5 w-3.5" />
                            </button>
                          </>
                        )}
                      </div>
                    </div>
                  </CardHeader>

                  <CardContent className="p-4 pt-3 space-y-2.5">
                    {skill.description && (
                      <p className="text-xs text-[var(--text-secondary)] line-clamp-2">
                        {skill.description}
                      </p>
                    )}
                    <pre className="bg-[var(--bg-surface)] rounded-xl p-3 font-mono text-xs text-[var(--text-primary)] whitespace-pre-wrap max-h-36 overflow-y-auto border border-[var(--border-subtle)]">
                      {skill.content}
                    </pre>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Tab 3: MCP Agent Simulator */}
      {activeTab === "simulator" && (
        <div className="space-y-4">
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-[var(--bg-secondary)] border border-[var(--border-subtle)] p-4 rounded-xl backdrop-blur-md">
            <div className="flex items-center gap-3">
              <Terminal className="h-5 w-5 text-emerald-400 shrink-0" />
              <div>
                <h3 className="text-sm font-semibold text-[var(--text-primary)]">
                  Simulador de Protocolo MCP en Vivo
                </h3>
                <p className="text-xs text-[var(--text-muted)]">
                  Respuesta idéntica que reciben Claude, Cursor y Windsurf al conectar al endpoint Streamable HTTP <code className="font-mono text-blue-400">/mcp</code>.
                </p>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={loadContextSimulator}
                disabled={contextLoading}
                className="text-xs gap-1.5"
              >
                <RefreshCw className={`h-3.5 w-3.5 ${contextLoading ? "animate-spin" : ""}`} />
                Refrescar
              </Button>
              {projectContext && (
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => copyToClipboard(JSON.stringify(projectContext, null, 2), "json_sim")}
                  className="text-xs gap-1.5"
                >
                  {copiedKey === "json_sim" ? <Check className="h-3.5 w-3.5 text-emerald-400" /> : <Copy className="h-3.5 w-3.5" />}
                  Copiar JSON
                </Button>
              )}
            </div>
          </div>

          {contextLoading ? (
            <div className="py-16 text-center text-sm text-[var(--text-muted)]">
              Consultando MCP Project Context Protocol...
            </div>
          ) : projectContext ? (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Consolidate System Prompt */}
              <Card className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
                <CardHeader className="p-4 pb-2 border-b border-[var(--border-subtle)]">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2 text-[var(--text-primary)]">
                      <ShieldCheck className="h-4 w-4 text-blue-400" />
                      System Prompt Consolidado
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      Markdown Injected
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-[var(--bg-surface)] p-4 rounded-xl text-xs font-mono text-[var(--text-primary)] whitespace-pre-wrap overflow-y-auto max-h-[420px] border border-[var(--border-subtle)]">
                    {projectContext.system_prompt}
                  </pre>
                </CardContent>
              </Card>

              {/* Skills Registry Payload */}
              <Card className="bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-xl">
                <CardHeader className="p-4 pb-2 border-b border-[var(--border-subtle)]">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2 text-[var(--text-primary)]">
                      <Sparkles className="h-4 w-4 text-amber-400" />
                      Skills Registrados ({projectContext.skills.length})
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      JSON Schema Registry
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-[var(--bg-surface)] p-4 rounded-xl text-xs font-mono text-[var(--text-primary)] whitespace-pre-wrap overflow-y-auto max-h-[420px] border border-[var(--border-subtle)]">
                    {JSON.stringify(projectContext.skills, null, 2)}
                  </pre>
                </CardContent>
              </Card>
            </div>
          ) : null}
        </div>
      )}

      {/* Tab 4: AI Rule & Skill Assistant */}
      {activeTab === "ai_assistant" && (
        <Card className="p-5 sm:p-7 bg-[var(--bg-secondary)] border-[var(--border-subtle)] shadow-2xl space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-[var(--border-subtle)]">
            <Wand2 className="h-5 w-5 text-purple-400" />
            <div>
              <h3 className="text-sm font-bold text-[var(--text-primary)]">
                Generador de Reglas & Skills Asistido por IA
              </h3>
              <p className="text-xs text-[var(--text-muted)]">
                Escribe en lenguaje natural el requerimiento o estándar que deseas imponer y la IA generará el artefacto listo para guardar.
              </p>
            </div>
          </div>

          <div className="space-y-3.5">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  TIPO DE ARTEFACTO
                </label>
                <Select
                  value={aiTargetKind}
                  onChange={(e) => setAiTargetKind(e.target.value as "rule" | "skill")}
                  className="h-9 text-xs w-full"
                >
                  <option value="rule">Regla de System Prompt</option>
                  <option value="skill">Skill Corporativo</option>
                </Select>
              </div>

              <div className="sm:col-span-2 space-y-1">
                <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                  PROYECTO DESTINO
                </label>
                <Input
                  type="text"
                  disabled
                  value={selectedProject || "Corporativo Global (Workspace)"}
                  className="h-9 text-xs bg-[var(--bg-surface)]"
                />
              </div>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-[var(--text-secondary)] block uppercase">
                DESCRIPCIÓN DEL ESTÁNDAR O PROCEDIMIENTO
              </label>
              <textarea
                rows={4}
                value={aiPrompt}
                onChange={(e) => setAiPrompt(e.target.value)}
                placeholder="ej: Todas las modificaciones en internal/platform/server deben validar autenticación mediante tokens y registrar trazas de auditoría..."
                className="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-3 text-xs font-mono text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:ring-1 focus:ring-purple-500"
              />
            </div>

            <div className="flex justify-end pt-2">
              <Button
                onClick={handleGenerateAiArtifact}
                disabled={!aiPrompt.trim() || isGeneratingAi}
                className="text-xs gap-1.5 bg-purple-600 hover:bg-purple-500 text-white shadow-lg shadow-purple-600/20"
              >
                <Sparkles className="h-4 w-4" />
                <span>Generar Artefacto con IA</span>
              </Button>
            </div>
          </div>
        </Card>
      )}

      {/* Create / Edit Artifact Modal */}
      <Dialog open={isModalOpen} onOpenChange={setIsModalOpen}>
        <DialogHeader>
          <DialogTitle>
            {editingArtifact
              ? `Editar ${modalKind === "rule" ? "Regla" : "Skill"}`
              : `Nuevo ${modalKind === "rule" ? "Regla de System Prompt" : "Skill Corporativo"}`}
          </DialogTitle>
          <DialogClose onClick={() => setIsModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleSave} className="space-y-4 mt-4">
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
                Tipo
              </label>
              <Select
                value={modalKind}
                onChange={(e) =>
                  setModalKind(e.target.value as "rule" | "skill")
                }
                className="w-full text-xs"
              >
                <option value="rule">Regla / System Prompt</option>
                <option value="skill">Skill Corporativo</option>
              </Select>
            </div>
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
                Alcance (Scope)
              </label>
              <Select
                value={modalScope}
                onChange={(e) =>
                  setModalScope(
                    e.target.value as "project" | "workspace_default",
                  )
                }
                className="w-full text-xs"
              >
                <option value="project">
                  Proyecto ({selectedProject || "actual"})
                </option>
                <option value="workspace_default">
                  Global (Workspace Default)
                </option>
              </Select>
            </div>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
                Clave Técnica (Key) *
              </label>
              <Input
                placeholder="ej: architecture_rules, deploy_k8s"
                value={modalKey}
                onChange={(e) => setModalKey(e.target.value)}
                required
                className="text-xs font-mono"
              />
            </div>
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
                Título Descriptivo *
              </label>
              <Input
                placeholder="ej: Directivas de Arquitectura Limpia"
                value={modalTitle}
                onChange={(e) => setModalTitle(e.target.value)}
                required
                className="text-xs"
              />
            </div>
          </div>

          {modalKind === "skill" && (
            <div>
              <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
                Descripción (para el descubrimiento del LLM)
              </label>
              <Input
                placeholder="Explica qué hace este skill y cuándo debe usarlo el agente"
                value={modalDesc}
                onChange={(e) => setModalDesc(e.target.value)}
                className="text-xs"
              />
            </div>
          )}

          <div>
            <label className="text-xs font-medium text-[var(--text-secondary)] block mb-1">
              Contenido / Instrucciones (Markdown) *
            </label>
            <textarea
              rows={6}
              value={modalContent}
              onChange={(e) => setModalContent(e.target.value)}
              placeholder="Escribe las directivas, reglas o procedimientos en Markdown..."
              required
              className="w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 py-2 text-xs font-mono text-[var(--text-primary)] placeholder:text-[var(--text-muted)] focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="flex flex-wrap items-center justify-end gap-2 pt-2 border-t border-[var(--border-subtle)]">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setIsModalOpen(false)}
              className="text-xs text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            >
              Cancelar
            </Button>
            <Button
              type="submit"
              variant="default"
              size="sm"
              disabled={saving}
              className="text-xs gap-1.5 shadow-md shadow-blue-500/20"
            >
              <CheckCircle2 className="h-3.5 w-3.5" />
              {saving ? "Guardando..." : "Guardar Artefacto"}
            </Button>
          </div>
        </form>
      </Dialog>

      {/* Read-Only Artifact Inspector Dialog */}
      <Dialog
        open={viewingArtifact !== null}
        onOpenChange={(open) => {
          if (!open) setViewingArtifact(null);
        }}
      >
        {viewingArtifact && (
          <>
            <DialogHeader>
              <div className="flex items-center justify-between gap-3 pr-6">
                <div className="flex items-center gap-2">
                  {viewingArtifact.kind === "rule" ? (
                    <div className="p-1.5 rounded-lg bg-blue-500/10 text-blue-400">
                      <ShieldCheck className="h-5 w-5" />
                    </div>
                  ) : (
                    <div className="p-1.5 rounded-lg bg-amber-500/10 text-amber-400">
                      <Sparkles className="h-5 w-5" />
                    </div>
                  )}
                  <div>
                    <DialogTitle className="text-base font-bold text-[var(--text-primary)]">
                      {viewingArtifact.title}
                    </DialogTitle>
                    <div className="flex items-center gap-2 mt-1">
                      <Badge
                        variant={viewingArtifact.scope === "workspace_default" ? "outline" : "secondary"}
                        className="text-[9px] px-1.5 py-0 font-mono"
                      >
                        {viewingArtifact.scope === "workspace_default" ? "Alcance Global" : `Proyecto: ${viewingArtifact.project || "default"}`}
                      </Badge>
                      <span className="text-[10px] font-mono text-[var(--text-muted)]">
                        key: {viewingArtifact.key}
                      </span>
                    </div>
                  </div>
                </div>
              </div>
              <DialogClose onClick={() => setViewingArtifact(null)} />
            </DialogHeader>

            <div className="space-y-4 mt-4">
              {viewingArtifact.description && (
                <div className="p-3 rounded-xl bg-[var(--bg-surface)] border border-[var(--border-subtle)] text-xs text-[var(--text-secondary)]">
                  <span className="font-semibold text-[var(--text-primary)] block mb-0.5">
                    Descripción / Propósito:
                  </span>
                  {viewingArtifact.description}
                </div>
              )}

              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <span className="text-xs font-semibold text-[var(--text-secondary)] uppercase">
                    Contenido / Instrucciones de Procedimiento
                  </span>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(viewingArtifact.content, `view-${viewingArtifact.id}`)}
                    className="text-[11px] text-blue-400 hover:text-blue-300 flex items-center gap-1 font-medium"
                  >
                    {copiedKey === `view-${viewingArtifact.id}` ? (
                      <>
                        <Check className="h-3 w-3 text-emerald-400" />
                        <span className="text-emerald-400">Copiado</span>
                      </>
                    ) : (
                      <>
                        <Copy className="h-3 w-3" />
                        <span>Copiar al portapapeles</span>
                      </>
                    )}
                  </button>
                </div>
                <pre className="bg-[var(--bg-surface)] rounded-xl p-4 font-mono text-xs text-[var(--text-primary)] whitespace-pre-wrap max-h-72 overflow-y-auto border border-[var(--border-subtle)] leading-relaxed">
                  {viewingArtifact.content}
                </pre>
              </div>

              <div className="p-3 rounded-xl bg-blue-500/5 border border-blue-500/20 text-xs text-[var(--text-secondary)] space-y-1">
                <div className="font-semibold text-blue-400 flex items-center gap-1.5">
                  <Info className="h-3.5 w-3.5" />
                  <span>Invocación desde tu Coding Agent (MCP):</span>
                </div>
                <p className="text-[11px] text-[var(--text-muted)] font-mono">
                  {viewingArtifact.kind === "rule"
                    ? `cortex_get_project_context(project: "${selectedProject || "default"}")`
                    : `cortex_get_skill(key: "${viewingArtifact.key}", project: "${selectedProject || "default"}")`}
                </p>
              </div>

              <div className="flex justify-end pt-2 border-t border-[var(--border-subtle)]">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => setViewingArtifact(null)}
                  className="text-xs"
                >
                  Cerrar
                </Button>
              </div>
            </div>
          </>
        )}
      </Dialog>
    </div>
  );
}
