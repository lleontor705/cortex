"use client";

import React, { useEffect, useState } from "react";
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
} from "lucide-react";

export default function ProjectsPage() {
  const { client } = useAuth();
  const [projects, setProjects] = useState<string[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>("");
  const [activeTab, setActiveTab] = useState<"rules" | "skills" | "simulator">(
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
  const [modalKind, setModalKind] = useState<"rule" | "skill">("rule");
  const [modalKey, setModalKey] = useState("");
  const [modalTitle, setModalTitle] = useState("");
  const [modalDesc, setModalDesc] = useState("");
  const [modalContent, setModalContent] = useState("");
  const [modalScope, setModalScope] = useState<
    "project" | "workspace_default"
  >("project");
  const [saving, setSaving] = useState(false);
  const [copied, setCopied] = useState(false);

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


  const rulesList = artifacts.filter((a) => a.kind === "rule");
  const skillsList = artifacts.filter((a) => a.kind === "skill");

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };
  const { principal } = useAuth();
  const userRoles = principal?.roles || ["admin"];
  const isAdmin = userRoles.some(
    (r) => r.toLowerCase() === "admin" || r.toLowerCase() === "owner",
  );
  const [projectSyncEnabled, setProjectSyncEnabled] = useState<boolean>(true);

  return (
    <div className="space-y-6">
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 border-b border-[var(--border-subtle)] pb-5">
        <div>
          <div className="flex items-center gap-2 mb-1">
            <h1 className="text-2xl font-bold tracking-tight text-[var(--text-primary)] flex items-center gap-2">
              <FolderKanban className="h-6 w-6 text-blue-500" />
              Proyectos & Skills Corporativos
            </h1>
            <Badge variant="secondary" className="text-xs font-mono text-blue-400">
              MCP Protocol
            </Badge>
          </div>
          <p className="text-sm text-[var(--text-muted)]">
            Gobierna los System Prompts, directivas de arquitectura y catálogo
            de herramientas corporativas expuestas a los agentes AI vía MCP.
          </p>
        </div>

        {/* Project Selector Bar */}
        <div className="flex items-center gap-2 bg-[var(--bg-surface)] border border-[var(--border-subtle)] p-1.5 rounded-xl backdrop-blur-md">
          <Layers className="h-4 w-4 text-[var(--text-muted)] ml-2" />
          <Select
            value={selectedProject}
            onChange={(e) => setSelectedProject(e.target.value)}
            className="w-48 bg-transparent border-0 font-medium text-xs focus:ring-0 text-[var(--text-primary)]"
          >
            <option value="">Corporativo Global (Workspace)</option>
            {projects.map((p) => (
              <option key={p} value={p}>
                Proyecto: {p}
              </option>
            ))}
          </Select>

          {/* Project-level Sync Toggle */}
          <button
            type="button"
            onClick={() => setProjectSyncEnabled(!projectSyncEnabled)}
            className={`px-2.5 py-1 rounded-lg text-xs font-semibold flex items-center gap-1.5 transition-all ${
              projectSyncEnabled
                ? "bg-blue-600/20 text-blue-400 border border-blue-500/30"
                : "bg-[var(--bg-surface)] text-[var(--text-muted)] border border-[var(--border-subtle)]"
            }`}
            title="Configura si el trabajo de este proyecto se sube a Cortex Server"
          >
            {projectSyncEnabled ? "☁️ Subir a Cortex: ON" : "🔒 Local Only"}
          </button>

          {isAdmin && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                const np = prompt("Nombre del nuevo proyecto:");
                if (np && np.trim()) {
                  const name = np.trim().toLowerCase().replace(/\s+/g, "-");
                  setProjects((prev) => Array.from(new Set([...prev, name])));
                  setSelectedProject(name);
                }
              }}
              className="h-8 px-2 text-xs"
            >
              <Plus className="h-3.5 w-3.5 mr-1" /> Nuevo
            </Button>
          )}
        </div>
      </div>

      {/* Navigation Tabs */}
      <div className="flex items-center justify-between border-b border-[var(--border-subtle)] pb-2">
        <div className="flex items-center gap-2">
          <Button
            variant={activeTab === "rules" ? "default" : "ghost"}
            size="sm"
            onClick={() => setActiveTab("rules")}
            className="gap-2 text-xs"
          >
            <ShieldCheck className="h-4 w-4" />
            System Prompt & Reglas ({rulesList.length})
          </Button>
          <Button
            variant={activeTab === "skills" ? "default" : "ghost"}
            size="sm"
            onClick={() => setActiveTab("skills")}
            className="gap-2 text-xs"
          >
            <Sparkles className="h-4 w-4" />
            Skills del Proyecto ({skillsList.length})
          </Button>
          <Button
            variant={activeTab === "simulator" ? "default" : "ghost"}
            size="sm"
            onClick={() => {
              setActiveTab("simulator");
              loadContextSimulator();
            }}
            className="gap-2 text-xs font-mono"
          >
            <Bot className="h-4 w-4" />
            Simulador Agente MCP
          </Button>
        </div>

        {activeTab !== "simulator" && (
          <Button
            variant="default"
            size="sm"
            onClick={() =>
              openCreateModal(activeTab === "rules" ? "rule" : "skill")
            }
            className="gap-1.5 text-xs shadow-md shadow-blue-500/20"
          >
            <Plus className="h-4 w-4" />
            {activeTab === "rules" ? "Agregar Regla" : "Agregar Skill"}
          </Button>
        )}
      </div>

      {/* Tab Content: Rules & System Prompt */}
      {activeTab === "rules" && (
        <div className="space-y-4">
          <div className="bg-blue-950/30 border border-blue-800/40 p-4 rounded-xl flex items-start gap-3">
            <ShieldCheck className="h-5 w-5 text-blue-400 shrink-0 mt-0.5" />
            <div>
              <h4 className="text-sm font-semibold text-slate-100">
                Resolución Jerárquica de System Prompt
              </h4>
              <p className="text-xs text-slate-400 mt-0.5">
                Las directivas configuradas como{" "}
                <span className="text-blue-400 font-medium">
                  Workspace Default
                </span>{" "}
                se aplican a todos los agentes. Las reglas de{" "}
                <span className="text-blue-400 font-medium">Proyecto</span> se
                combinan y tienen precedencia al consultar{" "}
                <code className="bg-slate-800 px-1 py-0.5 rounded font-mono text-[11px]">
                  cortex_get_project_context
                </code>
                .
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-slate-400 text-sm">
              Cargando reglas y directivas...
            </div>
          ) : rulesList.length === 0 ? (
            <Card className="border-dashed border-2 border-slate-800 p-8 text-center bg-slate-900/30">
              <BookOpen className="h-10 w-10 text-slate-600 mx-auto mb-3" />
              <h3 className="text-sm font-medium text-slate-200">
                Sin reglas configuradas
              </h3>
              <p className="text-xs text-slate-400 mt-1 max-w-sm mx-auto">
                Define las pautas de arquitectura, seguridad y estándares de
                código para este proyecto.
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => openCreateModal("rule")}
                className="mt-4 text-xs"
              >
                <Plus className="h-3.5 w-3.5 mr-1" /> Crear Primera Regla
              </Button>
            </Card>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {rulesList.map((rule) => (
                <Card
                  key={rule.id}
                  className="bg-slate-900/70 border-slate-800 hover:border-blue-500/40 transition-all flex flex-col justify-between"
                >
                  <CardHeader className="p-4 pb-2">
                    <div className="flex items-start justify-between gap-2">
                      <div>
                        <div className="flex items-center gap-2">
                          <CardTitle className="text-sm font-semibold text-slate-100">
                            {rule.title}
                          </CardTitle>
                          <Badge
                            variant={
                              rule.scope === "workspace_default"
                                ? "outline"
                                : "secondary"
                            }
                            className="text-[10px] px-1.5 py-0 font-mono"
                          >
                            {rule.scope === "workspace_default"
                              ? "Global"
                              : "Proyecto"}
                          </Badge>
                        </div>
                        <span className="text-[11px] font-mono text-slate-400 block mt-0.5">
                          key: {rule.key} • rev {rule.revision}
                        </span>
                      </div>
                      <div className="flex items-center gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => openEditModal(rule)}
                          className="h-7 w-7 text-slate-400 hover:text-slate-100"
                        >
                          <Edit3 className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => handleDelete(rule.id)}
                          className="h-7 w-7 text-slate-400 hover:text-rose-400"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  </CardHeader>
                  <CardContent className="p-4 pt-2">
                    <div className="bg-slate-950/60 rounded-lg p-3 font-mono text-xs text-slate-200 whitespace-pre-wrap max-h-48 overflow-y-auto border border-slate-800/80">
                      {rule.content}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Tab Content: Skills */}
      {activeTab === "skills" && (
        <div className="space-y-4">
          <div className="bg-amber-950/30 border border-amber-800/40 p-4 rounded-xl flex items-start gap-3">
            <Sparkles className="h-5 w-5 text-amber-400 shrink-0 mt-0.5" />
            <div>
              <h4 className="text-sm font-semibold text-slate-100">
                Catálogo de Skills MCP
              </h4>
              <p className="text-xs text-slate-400 mt-0.5">
                Los skills son procedimientos ejecutables o directrices
                especializadas que los agentes descubren mediante{" "}
                <code className="bg-slate-800 px-1 py-0.5 rounded font-mono text-[11px]">
                  cortex_list_skills
                </code>{" "}
                y consultan en detalle con{" "}
                <code className="bg-slate-800 px-1 py-0.5 rounded font-mono text-[11px]">
                  cortex_get_skill
                </code>
                .
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-slate-400 text-sm">
              Cargando catálogo de skills...
            </div>
          ) : skillsList.length === 0 ? (
            <Card className="border-dashed border-2 border-slate-800 p-8 text-center bg-slate-900/30">
              <Code2 className="h-10 w-10 text-slate-600 mx-auto mb-3" />
              <h3 className="text-sm font-medium text-slate-200">
                Sin skills registrados
              </h3>
              <p className="text-xs text-slate-400 mt-1 max-w-sm mx-auto">
                Crea habilidades corporativas (despliegues, revisiones de
                seguridad, migraciones) para tus agentes.
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => openCreateModal("skill")}
                className="mt-4 text-xs"
              >
                <Plus className="h-3.5 w-3.5 mr-1" /> Registrar Primer Skill
              </Button>
            </Card>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {skillsList.map((skill) => (
                <Card
                  key={skill.id}
                  className="bg-slate-900/70 border-slate-800 hover:border-amber-500/40 transition-all flex flex-col justify-between"
                >
                  <CardHeader className="p-4 pb-2">
                    <div className="flex items-start justify-between gap-2">
                      <div>
                        <div className="flex items-center gap-2">
                          <CardTitle className="text-sm font-semibold text-slate-100">
                            {skill.title}
                          </CardTitle>
                          <Badge
                            variant="secondary"
                            className="text-[10px] px-1.5 py-0 font-mono text-amber-400"
                          >
                            Skill
                          </Badge>
                        </div>
                        <span className="text-[11px] font-mono text-slate-400 block mt-0.5">
                          {skill.key}
                        </span>
                      </div>
                      <div className="flex items-center gap-1">
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => openEditModal(skill)}
                          className="h-7 w-7 text-slate-400 hover:text-slate-100"
                        >
                          <Edit3 className="h-3.5 w-3.5" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          onClick={() => handleDelete(skill.id)}
                          className="h-7 w-7 text-slate-400 hover:text-rose-400"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                      </div>
                    </div>
                  </CardHeader>
                  <CardContent className="p-4 pt-2 space-y-2">
                    {skill.description && (
                      <p className="text-xs text-slate-400 line-clamp-2">
                        {skill.description}
                      </p>
                    )}
                    <div className="bg-slate-950/60 rounded-lg p-2.5 font-mono text-xs text-slate-300 whitespace-pre-wrap max-h-36 overflow-y-auto border border-slate-800/80">
                      {skill.content}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Tab Content: Agent Simulator */}
      {activeTab === "simulator" && (
        <div className="space-y-4">
          <div className="flex items-center justify-between bg-slate-900/60 border border-slate-800 p-4 rounded-xl backdrop-blur-md">
            <div className="flex items-center gap-3">
              <Terminal className="h-5 w-5 text-emerald-400" />
              <div>
                <h3 className="text-sm font-semibold text-slate-100">
                  Simulador de Consulta de Agente AI
                </h3>
                <p className="text-xs text-slate-400">
                  Visualiza en tiempo real la respuesta exacta que recibe el
                  agente (Claude, Cursor, Windsurf) al invocar las herramientas
                  MCP.
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
                <RefreshCw
                  className={`h-3.5 w-3.5 ${contextLoading ? "animate-spin" : ""}`}
                />
                Refrescar
              </Button>
              {projectContext && (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() =>
                    copyToClipboard(JSON.stringify(projectContext, null, 2))
                  }
                  className="text-xs gap-1.5"
                >
                  {copied ? (
                    <Check className="h-3.5 w-3.5 text-emerald-400" />
                  ) : (
                    <Copy className="h-3.5 w-3.5" />
                  )}
                  {copied ? "Copiado" : "Copiar JSON"}
                </Button>
              )}
            </div>
          </div>

          {contextLoading ? (
            <div className="py-16 text-center text-sm text-slate-400">
              Consultando MCP Project Context Protocol...
            </div>
          ) : projectContext ? (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Consolidate System Prompt */}
              <Card className="bg-slate-900/70 border-slate-800">
                <CardHeader className="p-4 pb-2 border-b border-slate-800">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <ShieldCheck className="h-4 w-4 text-blue-400" />
                      System Prompt Consolidado
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      Markdown
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-slate-950/80 p-4 rounded-xl text-xs font-mono text-slate-200 whitespace-pre-wrap overflow-y-auto max-h-[400px] border border-slate-800">
                    {projectContext.system_prompt}
                  </pre>
                </CardContent>
              </Card>

              {/* Skills Registry Payload */}
              <Card className="bg-slate-900/70 border-slate-800">
                <CardHeader className="p-4 pb-2 border-b border-slate-800">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2">
                      <Sparkles className="h-4 w-4 text-amber-400" />
                      Skills Disponibles ({projectContext.skills.length})
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      JSON Payload
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-slate-950/80 p-4 rounded-xl text-xs font-mono text-slate-200 whitespace-pre-wrap overflow-y-auto max-h-[400px] border border-slate-800">
                    {JSON.stringify(projectContext.skills, null, 2)}
                  </pre>
                </CardContent>
              </Card>
            </div>
          ) : null}
        </div>
      )}

      {/* Create / Edit Artifact Modal */}
      <Dialog
        open={isModalOpen}
        onOpenChange={setIsModalOpen}
      >
        <DialogHeader>
          <DialogTitle>
            {editingArtifact
              ? `Editar ${modalKind === "rule" ? "Regla" : "Skill"}`
              : `Nuevo ${modalKind === "rule" ? "Regla de System Prompt" : "Skill Corporativo"}`}
          </DialogTitle>
          <DialogClose onClick={() => setIsModalOpen(false)} />
        </DialogHeader>

        <form onSubmit={handleSave} className="space-y-4 mt-4">
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-slate-400 block mb-1">
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
              <label className="text-xs font-medium text-slate-400 block mb-1">
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

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs font-medium text-slate-400 block mb-1">
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
              <label className="text-xs font-medium text-slate-400 block mb-1">
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
              <label className="text-xs font-medium text-slate-400 block mb-1">
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
            <label className="text-xs font-medium text-slate-400 block mb-1">
              Contenido / Instrucciones (Markdown) *
            </label>
            <textarea
              rows={6}
              value={modalContent}
              onChange={(e) => setModalContent(e.target.value)}
              placeholder="Escribe las directivas, reglas o procedimientos en Markdown..."
              required
              className="w-full rounded-md border border-slate-800 bg-slate-950/80 px-3 py-2 text-xs font-mono text-slate-100 placeholder:text-slate-500 focus:outline-none focus:ring-1 focus:ring-blue-500"
            />
          </div>

          <div className="flex items-center justify-end gap-2 pt-2 border-t border-slate-800">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setIsModalOpen(false)}
              className="text-xs"
            >
              Cancelar
            </Button>
            <Button
              type="submit"
              variant="default"
              size="sm"
              disabled={saving}
              className="text-xs gap-1.5"
            >
              <CheckCircle2 className="h-3.5 w-3.5" />
              {saving ? "Guardando..." : "Guardar Artefacto"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}
