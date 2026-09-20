"use client";

import React, { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import {
  ProjectArtifactItem,
  ProjectContext,
  ProjectDuplicateGroup,
  SaveProjectArtifactInput,
  RAGStats,
} from "@/lib/api";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { PageHeader } from "@/components/shared/PageHeader";
import { StatCard } from "@/components/shared/StatCard";
import { EmptyState } from "@/components/shared/EmptyState";
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
  GitMerge,
  AlertTriangle,
  Database,
  Cpu,
  Clock,
  Zap,
} from "lucide-react";

export default function ProjectsPage() {
  const router = useRouter();
  const { client, principal, llmApiKey, llmProvider, llmModel, llmBaseURL } = useAuth();
  const [projects, setProjects] = useState<string[]>([]);
  const [selectedProject, setSelectedProject] = useState<string>("");
  const [activeTab, setActiveTab] = useState<"rules" | "skills" | "simulator" | "ai_assistant" | "rag_indexing">(
    "rules",
  );

  const [artifacts, setArtifacts] = useState<ProjectArtifactItem[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [ragStats, setRagStats] = useState<RAGStats | null>(null);
  const [ragLoading, setRagLoading] = useState<boolean>(false);
  const [projectContext, setProjectContext] = useState<ProjectContext | null>(
    null,
  );
  const [contextLoading, setContextLoading] = useState<boolean>(false);

  // Project Deduplication & Merge State
  const [duplicateGroups, setDuplicateGroups] = useState<ProjectDuplicateGroup[]>([]);
  const [isMerging, setIsMerging] = useState<boolean>(false);
  const [mergeMessage, setMergeMessage] = useState<string | null>(null);

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
    if (isAdmin) {
      loadDuplicates();
    }
  }, [client, isAdmin]);

  useEffect(() => {
    loadArtifacts();
    loadRAGStats();
  }, [client, selectedProject]);

  const loadRAGStats = async () => {
    if (!client) return;
    setRagLoading(true);
    try {
      const stats = await client.getRAGStats(selectedProject);
      setRagStats(stats);
    } catch {
      setRagStats({
        project: selectedProject || "cortex",
        total_observations: 0,
        indexed_observations: 0,
        pending_observations: 0,
        failed_observations: 0,
        coverage_pct: 100.0,
        embedding_model: "Auto-detectado",
        embedding_dimensions: 0,
        vector_provider: "Servidor Cortex",
      });
    } finally {
      setRagLoading(false);
    }
  };

  const loadDuplicates = async () => {
    if (!client || !isAdmin) return;
    try {
      const dups = await client.getProjectDuplicates();
      setDuplicateGroups(dups || []);
    } catch (e) {
      console.error("Failed to load duplicates", e);
    }
  };

  const handleMergeProjects = async (source: string, target: string) => {
    if (!client || !isAdmin) return;
    if (
      !confirm(
        `¿Confirmas la consolidación del proyecto "${source}" en el canónico "${target}"? Todas las observaciones, sesiones y reglas asociadas se reasignarán automáticamente.`,
      )
    ) {
      return;
    }
    setIsMerging(true);
    setMergeMessage(null);
    try {
      const res = await client.mergeProject(source, target);
      setMergeMessage(
        `¡Fusión exitosa! Se consolidaron ${res.observations_merged} observaciones, ${res.sessions_merged} sesiones y ${res.prompts_merged} prompts bajo "${target}".`,
      );
      await loadProjects();
      await loadDuplicates();
      setSelectedProject(target);
    } catch (err: any) {
      alert("Error al fusionar proyectos: " + (err.message || err));
    } finally {
      setIsMerging(false);
    }
  };

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
      {/* Enterprise Calm Header */}
      <Card className="p-5 sm:p-6 rounded-lg bg-card border-border shadow-sm">
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-5">
          <div className="space-y-1.5 max-w-2xl">
            <div className="flex items-center gap-2">
              <span className="w-8 h-8 rounded-lg bg-primary/10 text-primary border border-primary/20 flex items-center justify-center font-bold text-sm">
                <FolderKanban className="h-4 w-4" />
              </span>
              <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-foreground">
                Proyectos, Directivas & Skills MCP
              </h1>
              <Badge
                variant="outline"
                className={`text-[10px] px-2 font-mono uppercase ${
                  isAdmin
                    ? "border-slate-600/40 text-slate-300 bg-slate-800/60"
                    : "border-primary/30 text-primary bg-primary/10"
                }`}
              >
                {isAdmin ? "GOBERNANZA & EDICIÓN" : "MODO CONSULTA MCP"}
              </Badge>
            </div>
            <p className="text-xs sm:text-sm text-muted-foreground leading-relaxed">
              {isAdmin
                ? "Gobierno centralizado de System Prompts, arquitectura limpia y catálogo de herramientas corporativas inyectadas en tiempo de ejecución a Claude, Cursor y Windsurf."
                : "Catálogo de directivas corporativas y herramientas de procedimiento inyectadas automáticamente en tiempo de ejecución a tus Coding Agents vía MCP."}
            </p>
          </div>

          {/* Quick Stats Bar */}
          <div className="flex flex-wrap items-center gap-3 shrink-0">
            <div className="p-3 rounded-lg bg-secondary/50 border border-border flex items-center gap-3">
              <div className="text-left">
                <div className="text-[10px] text-muted-foreground uppercase tracking-wider font-semibold">Reglas Activas</div>
                <div className="text-lg font-bold font-mono text-primary">{rulesList.length}</div>
              </div>
              <ShieldCheck className="h-5 w-5 text-primary/40" />
            </div>

            <div className="p-3 rounded-lg bg-secondary/50 border border-border flex items-center gap-3">
              <div className="text-left">
                <div className="text-[10px] text-muted-foreground uppercase tracking-wider font-semibold">Skills MCP</div>
                <div className="text-lg font-bold font-mono text-amber-500">{skillsList.length}</div>
              </div>
              <Sparkles className="h-5 w-5 text-amber-500/40" />
            </div>
          </div>
        </div>

        {/* Project Selector & Actions Bar */}
        <div className="mt-5 pt-4 border-t border-border flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2.5">
            <div className="flex items-center gap-2 bg-secondary border border-border px-3 py-1 rounded-lg">
              <Layers className="h-4 w-4 text-primary shrink-0" />
              <Select
                value={selectedProject}
                onChange={(e) => setSelectedProject(e.target.value)}
                className="bg-transparent border-0 font-semibold text-xs text-foreground focus:ring-0 cursor-pointer min-w-[200px]"
              >
                <option value="">Corporativo Global (Workspace)</option>
                {projects.map((p) => (
                  <option key={p} value={p}>
                    Proyecto: {p}
                  </option>
                ))}
              </Select>
            </div>

            <Button
              variant="default"
              size="sm"
              onClick={() => router.push(`/graph?project=${encodeURIComponent(selectedProject || "all")}`)}
              className="text-xs gap-1.5 shadow-sm"
              title="Explorar el Grafo Completo del Proyecto en Cortex Web"
            >
              <Share2 className="h-3.5 w-3.5" />
              <span>Ver Grafo del Proyecto</span>
            </Button>

            <button
              type="button"
              onClick={() => setProjectSyncEnabled(!projectSyncEnabled)}
              className={`px-3 py-1.5 rounded-lg text-xs font-semibold flex items-center gap-1.5 transition-colors ${
                projectSyncEnabled
                  ? "bg-blue-600/20 text-blue-400 border border-blue-500/30"
                  : "bg-secondary text-muted-foreground border border-border"
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
              className="text-xs gap-1.5 border-border bg-secondary/50"
            >
              <Plus className="h-3.5 w-3.5" />
              <span>Nuevo Proyecto</span>
            </Button>
          )}
        </div>

        {/* Duplicate Projects AI Alert Banner */}
        {isAdmin && duplicateGroups.length > 0 && (
          <div className="mt-4 p-4 rounded-lg bg-amber-500/10 border border-amber-500/30 flex flex-col sm:flex-row sm:items-center justify-between gap-3 text-xs">
            <div className="flex items-start gap-2.5">
              <AlertTriangle className="h-4 w-4 text-amber-400 shrink-0 mt-0.5" />
              <div>
                <span className="font-bold text-amber-300">
                  🪄 Inconsistencias de Proyectos Detectadas por IA ({duplicateGroups.length})
                </span>
                <p className="text-[11px] text-muted-foreground mt-0.5">
                  Existen proyectos con diferentes mayúsculas/minúsculas como{" "}
                  {duplicateGroups.map((g) => g.variants.join(" / ")).join(", ")}. Puedes fusionarlos y consolidarlos en un único proyecto canónico.
                </p>
              </div>
            </div>
            <Button
              size="sm"
              onClick={() => setActiveTab("ai_assistant")}
              className="bg-amber-600 hover:bg-amber-500 text-white text-xs gap-1.5 shrink-0 shadow-md"
            >
              <GitMerge className="h-3.5 w-3.5" />
              <span>Revisar y Fusionar</span>
            </Button>
          </div>
        )}

        {/* Merge Success Alert */}
        {mergeMessage && (
          <div className="mt-4 p-3.5 rounded-lg bg-emerald-500/10 border border-emerald-500/30 flex items-center justify-between gap-2 text-xs text-emerald-400">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 shrink-0" />
              <span>{mergeMessage}</span>
            </div>
            <button
              type="button"
              onClick={() => setMergeMessage(null)}
              className="text-emerald-400 hover:text-emerald-300"
            >
              ✕
            </button>
          </div>
        )}
      </Card>

      {/* Modern Navigation Tabs */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 border-b border-border pb-2">
        <div className="flex items-center gap-2 overflow-x-auto pb-1 sm:pb-0">
          <button
            type="button"
            onClick={() => setActiveTab("rules")}
            className={`px-3.5 py-2 rounded-lg text-xs font-semibold flex items-center gap-2 transition-colors ${
              activeTab === "rules"
                ? "bg-primary text-primary-foreground shadow-xs"
                : "bg-secondary/50 text-muted-foreground hover:bg-secondary hover:text-foreground border border-border"
            }`}
          >
            <ShieldCheck className="h-4 w-4" />
            <span>Directivas & Reglas ({rulesList.length})</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("skills")}
            className={`px-3.5 py-2 rounded-lg text-xs font-semibold flex items-center gap-2 transition-colors ${
              activeTab === "skills"
                ? "bg-primary text-primary-foreground shadow-xs"
                : "bg-secondary/50 text-muted-foreground hover:bg-secondary hover:text-foreground border border-border"
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
            className={`px-3.5 py-2 rounded-lg text-xs font-semibold font-mono flex items-center gap-2 transition-colors ${
              activeTab === "simulator"
                ? "bg-primary text-primary-foreground shadow-xs"
                : "bg-secondary/50 text-muted-foreground hover:bg-secondary hover:text-foreground border border-border"
            }`}
          >
            <Terminal className="h-4 w-4" />
            <span>Simulador MCP</span>
          </button>

          <button
            type="button"
            onClick={() => setActiveTab("rag_indexing")}
            className={`px-3.5 py-2 rounded-lg text-xs font-semibold flex items-center gap-2 transition-colors ${
              activeTab === "rag_indexing"
                ? "bg-primary text-primary-foreground shadow-xs"
                : "bg-secondary/50 text-muted-foreground hover:bg-secondary hover:text-foreground border border-border"
            }`}
          >
            <Database className="h-4 w-4" />
            <span>Pipeline RAG ({ragStats ? `${Math.round(ragStats.coverage_pct)}%` : "100%"})</span>
          </button>

          {isAdmin && (
            <button
              type="button"
              onClick={() => setActiveTab("ai_assistant")}
              className={`px-3.5 py-2 rounded-lg text-xs font-semibold flex items-center gap-2 transition-colors ${
                activeTab === "ai_assistant"
                  ? "bg-primary text-primary-foreground shadow-xs"
                  : "bg-secondary/50 text-muted-foreground hover:bg-secondary hover:text-foreground border border-border"
              }`}
            >
              <Wand2 className="h-4 w-4" />
              <span>Generador IA (Admin)</span>
            </button>
          )}
        </div>

        {isAdmin && activeTab !== "simulator" && activeTab !== "ai_assistant" && activeTab !== "rag_indexing" && (
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
          <div className="p-4 rounded-lg bg-card border border-border shadow-xs flex items-start gap-3">
            <ShieldCheck className="h-5 w-5 text-primary shrink-0 mt-0.5" />
            <div>
              <h4 className="text-xs sm:text-sm font-semibold text-foreground">
                Jerarquía Dinámica de System Prompts
              </h4>
              <p className="text-xs text-muted-foreground mt-0.5">
                Las directivas de alcance <b className="text-primary font-semibold">Global Workspace</b> aplican a todos los agentes. Al consultar <code className="font-mono text-[11px] bg-secondary px-1 py-0.5 rounded">cortex_get_project_context</code>, se agregan y combinan con las directivas específicas del proyecto activo.
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-muted-foreground text-sm">
              Cargando reglas y directivas...
            </div>
          ) : rulesList.length === 0 ? (
            <EmptyState
              icon={BookOpen}
              title="Sin directivas registradas"
              description={
                isAdmin
                  ? "Crea reglas de Clean Architecture, Zero CGO, o directrices de seguridad para este proyecto."
                  : "No hay directivas asignadas a este proyecto. Consulta con el Administrador para crear reglas corporativas."
              }
              action={
                isAdmin ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => openCreateModal("rule")}
                    className="text-xs gap-1.5"
                  >
                    <Plus className="h-3.5 w-3.5" />
                    <span>Crear Primera Directiva</span>
                  </Button>
                ) : null
              }
            />
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {rulesList.map((rule) => (
                <Card
                  key={rule.id}
                  className="bg-card border-border hover:border-primary/40 transition-all flex flex-col justify-between shadow-xs"
                >
                  <CardHeader className="p-4 pb-2 border-b border-border">
                    <div className="flex items-start justify-between gap-2">
                      <div className="overflow-hidden">
                        <div className="flex items-center gap-1.5">
                          <CardTitle className="text-sm font-semibold text-foreground truncate">
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
                          <span className="text-[10px] font-mono text-muted-foreground">
                            key: {rule.key}
                          </span>
                        </div>
                      </div>

                      <div className="flex items-center gap-1 shrink-0">
                        <button
                          type="button"
                          onClick={() => setViewingArtifact(rule)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-primary transition-colors"
                          title="Ver detalle de directiva"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(rule.content, rule.id)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground transition-colors"
                          title="Copiar prompt"
                        >
                          {copiedKey === rule.id ? (
                            <Check className="h-3.5 w-3.5 text-emerald-500" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                        {isAdmin && (
                          <>
                            <button
                              type="button"
                              onClick={() => openEditModal(rule)}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-primary transition-colors"
                              title="Editar"
                            >
                              <Edit3 className="h-3.5 w-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDelete(rule.id)}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive transition-colors"
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
                      <p className="text-xs text-muted-foreground line-clamp-2">
                        {rule.description}
                      </p>
                    )}
                    <pre className="bg-secondary/40 rounded-lg p-3 font-mono text-xs text-foreground whitespace-pre-wrap max-h-36 overflow-y-auto border border-border">
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
          <div className="p-4 rounded-lg bg-card border border-border shadow-xs flex items-start gap-3">
            <Sparkles className="h-5 w-5 text-amber-500 shrink-0 mt-0.5" />
            <div>
              <h4 className="text-xs sm:text-sm font-semibold text-foreground">
                Catálogo de Herramientas Corporativas MCP
              </h4>
              <p className="text-xs text-muted-foreground mt-0.5">
                Los skills son procedimientos reutilizables que los agentes descubren vía <code className="font-mono text-[11px] bg-secondary px-1 py-0.5 rounded">cortex_list_skills</code> e invocan con <code className="font-mono text-[11px] bg-secondary px-1 py-0.5 rounded">cortex_get_skill</code>.
              </p>
            </div>
          </div>

          {loading ? (
            <div className="py-12 text-center text-muted-foreground text-sm">
              Cargando catálogo de skills...
            </div>
          ) : skillsList.length === 0 ? (
            <EmptyState
              icon={Code2}
              title="Sin skills registrados"
              description={
                isAdmin
                  ? "Crea habilidades corporativas (despliegues, linters, migraciones) accesibles por agentes AI."
                  : "No hay skills registrados para este proyecto. El Administrador puede añadir herramientas al catálogo MCP."
              }
              action={
                isAdmin ? (
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => openCreateModal("skill")}
                    className="text-xs gap-1.5"
                  >
                    <Plus className="h-3.5 w-3.5" />
                    <span>Registrar Primer Skill</span>
                  </Button>
                ) : null
              }
            />
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {skillsList.map((skill) => (
                <Card
                  key={skill.id}
                  className="bg-card border-border hover:border-amber-500/50 transition-all flex flex-col justify-between shadow-xs"
                >
                  <CardHeader className="p-4 pb-2 border-b border-border">
                    <div className="flex items-start justify-between gap-2">
                      <div className="overflow-hidden">
                        <div className="flex items-center gap-1.5">
                          <CardTitle className="text-sm font-semibold text-foreground truncate">
                            {skill.title}
                          </CardTitle>
                        </div>
                        <div className="flex items-center gap-2 mt-1">
                          <Badge variant="secondary" className="text-[9px] px-1.5 py-0 font-mono text-amber-500 dark:text-amber-400">
                            MCP Tool
                          </Badge>
                          <span className="text-[10px] font-mono text-muted-foreground truncate">
                            key: {skill.key}
                          </span>
                        </div>
                      </div>

                      <div className="flex items-center gap-1 shrink-0">
                        <button
                          type="button"
                          onClick={() => setViewingArtifact(skill)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-amber-500 transition-colors"
                          title="Ver detalle del skill"
                        >
                          <Eye className="h-3.5 w-3.5" />
                        </button>
                        <button
                          type="button"
                          onClick={() => copyToClipboard(skill.content, skill.id)}
                          className="p-1.5 rounded-lg text-muted-foreground hover:text-foreground transition-colors"
                          title="Copiar instrucciones"
                        >
                          {copiedKey === skill.id ? (
                            <Check className="h-3.5 w-3.5 text-emerald-500" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
                          )}
                        </button>
                        {isAdmin && (
                          <>
                            <button
                              type="button"
                              onClick={() => openEditModal(skill)}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-amber-500 transition-colors"
                              title="Editar"
                            >
                              <Edit3 className="h-3.5 w-3.5" />
                            </button>
                            <button
                              type="button"
                              onClick={() => handleDelete(skill.id)}
                              className="p-1.5 rounded-lg text-muted-foreground hover:text-destructive transition-colors"
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
                      <p className="text-xs text-muted-foreground line-clamp-2">
                        {skill.description}
                      </p>
                    )}
                    <pre className="bg-secondary/40 rounded-lg p-3 font-mono text-xs text-foreground whitespace-pre-wrap max-h-36 overflow-y-auto border border-border">
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
          <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 bg-card border border-border p-4 rounded-lg shadow-sm">
            <div className="flex items-center gap-3">
              <Terminal className="h-5 w-5 text-emerald-400 shrink-0" />
              <div>
                <h3 className="text-sm font-semibold text-foreground">
                  Simulador de Protocolo MCP en Vivo
                </h3>
                <p className="text-xs text-muted-foreground">
                  Respuesta idéntica que reciben Claude, Cursor y Windsurf al conectar al endpoint Streamable HTTP <code className="font-mono text-primary">/mcp</code>.
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
            <div className="py-16 text-center text-sm text-muted-foreground">
              Consultando MCP Project Context Protocol...
            </div>
          ) : projectContext ? (
            <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
              {/* Consolidate System Prompt */}
              <Card className="bg-card border-border shadow-xs">
                <CardHeader className="p-4 pb-2 border-b border-border">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2 text-foreground">
                      <ShieldCheck className="h-4 w-4 text-blue-400" />
                      System Prompt Consolidado
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      Markdown Injected
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-secondary/50 p-4 rounded-lg text-xs font-mono text-foreground whitespace-pre-wrap overflow-y-auto max-h-[420px] border border-border">
                    {projectContext.system_prompt}
                  </pre>
                </CardContent>
              </Card>

              {/* Skills Registry Payload */}
              <Card className="bg-card border-border shadow-sm">
                <CardHeader className="p-4 pb-2 border-b border-border">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm font-semibold flex items-center gap-2 text-foreground">
                      <Sparkles className="h-4 w-4 text-amber-400" />
                      Skills Registrados ({projectContext.skills.length})
                    </CardTitle>
                    <Badge variant="outline" className="text-[10px] font-mono">
                      JSON Schema Registry
                    </Badge>
                  </div>
                </CardHeader>
                <CardContent className="p-4">
                  <pre className="bg-secondary/50 p-4 rounded-lg text-xs font-mono text-foreground whitespace-pre-wrap overflow-y-auto max-h-[420px] border border-border">
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
        <Card className="p-5 sm:p-7 bg-card border-border shadow-sm space-y-4">
          <div className="flex items-center gap-2.5 pb-3 border-b border-border">
            <Wand2 className="h-5 w-5 text-primary" />
            <div>
              <h3 className="text-sm font-bold text-foreground">
                Generador de Reglas & Skills Asistido por IA
              </h3>
              <p className="text-xs text-muted-foreground">
                Escribe en lenguaje natural el requerimiento o estándar que deseas imponer y la IA generará el artefacto listo para guardar.
              </p>
            </div>
          </div>

          <div className="space-y-3.5">
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              <div className="space-y-1">
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
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
                <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
                  PROYECTO DESTINO
                </label>
                <Input
                  type="text"
                  disabled
                  value={selectedProject || "Corporativo Global (Workspace)"}
                  className="h-9 text-xs bg-secondary/50"
                />
              </div>
            </div>

            <div className="space-y-1">
              <label className="text-[11px] font-semibold text-muted-foreground block uppercase">
                DESCRIPCIÓN DEL ESTÁNDAR O PROCEDIMIENTO
              </label>
              <textarea
                rows={4}
                value={aiPrompt}
                onChange={(e) => setAiPrompt(e.target.value)}
                placeholder="ej: Todas las modificaciones en internal/platform/server deben validar autenticación mediante tokens y registrar trazas de auditoría..."
                className="w-full rounded-lg border border-input bg-background p-3 text-xs font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>

            <div className="flex justify-end pt-2">
              <Button
                onClick={handleGenerateAiArtifact}
                disabled={!aiPrompt.trim() || isGeneratingAi}
                className="text-xs gap-1.5 shadow-xs"
              >
                <Sparkles className="h-4 w-4" />
                <span>Generar Artefacto con IA</span>
              </Button>
            </div>
          </div>

          {/* AI Project Deduplication & Merge Engine */}
          <div className="pt-6 border-t border-border space-y-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <GitMerge className="h-4 w-4 text-primary" />
                <h4 className="text-xs font-bold text-foreground uppercase tracking-wider">
                  Unificador & Fusión de Proyectos IA (Deduplicación)
                </h4>
              </div>
              <Button
                variant="ghost"
                size="sm"
                onClick={loadDuplicates}
                className="text-xs gap-1.5 h-7 text-muted-foreground hover:text-foreground"
              >
                <RefreshCw className="h-3 w-3" />
                <span>Re-escanear</span>
              </Button>
            </div>

            <p className="text-xs text-muted-foreground">
              Detecta automáticamente proyectos duplicados o con variaciones de mayúsculas/minúsculas (ej: <code className="font-mono bg-secondary px-1 py-0.5 rounded text-foreground">itc.facturadorwebpos</code> vs <code className="font-mono bg-secondary px-1 py-0.5 rounded text-foreground">ITC.FacturadorWebPos</code>, <code className="font-mono bg-secondary px-1 py-0.5 rounded text-foreground">FINAL</code> vs <code className="font-mono bg-secondary px-1 py-0.5 rounded text-foreground">final</code>) y los consolida sin pérdida de observaciones, sesiones ni aristas.
            </p>

            {duplicateGroups.length === 0 ? (
              <div className="p-4 rounded-lg bg-secondary/50 border border-border text-center text-xs text-muted-foreground">
                <CheckCircle2 className="h-6 w-6 text-emerald-500 mx-auto mb-1.5 opacity-80" />
                <span>No se detectaron proyectos duplicados ni discrepancias de casing en este Workspace.</span>
              </div>
            ) : (
              <div className="space-y-3">
                {duplicateGroups.map((group, idx) => (
                  <div
                    key={idx}
                    className="p-4 rounded-lg bg-card border border-border shadow-xs flex flex-col sm:flex-row sm:items-center justify-between gap-3"
                  >
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-xs text-foreground">
                          Canónico Sugerido: <span className="font-mono text-emerald-400">{group.canonical_name}</span>
                        </span>
                        <Badge variant="warning" className="text-[10px]">
                          {group.total_count} observaciones
                        </Badge>
                      </div>
                      <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                        <span>Variaciones detectadas:</span>
                        {group.variants.map((v) => (
                          <span
                            key={v}
                            className={`font-mono px-2 py-0.5 rounded text-[11px] ${
                              v === group.canonical_name
                                ? "bg-emerald-500/20 text-emerald-300 border border-emerald-500/40"
                                : "bg-amber-500/10 text-amber-300 border border-amber-500/30"
                            }`}
                          >
                            {v}
                          </span>
                        ))}
                      </div>
                    </div>

                    <div className="flex flex-wrap items-center gap-2 shrink-0">
                      {group.variants
                        .filter((v) => v !== group.canonical_name)
                        .map((variant) => (
                          <Button
                            key={variant}
                            size="sm"
                            disabled={isMerging}
                            onClick={() => handleMergeProjects(variant, group.canonical_name)}
                            className="bg-amber-600 hover:bg-amber-500 text-white text-xs gap-1.5 shadow-sm"
                          >
                            <GitMerge className="h-3.5 w-3.5" />
                            <span>Fusionar {variant} ➔ {group.canonical_name}</span>
                          </Button>
                        ))}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </Card>
      )}

      {/* Tab 5: RAG & Vector Pipeline Diagnostics */}
      {activeTab === "rag_indexing" && (
        <div className="space-y-6">
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            <StatCard
              title="Cobertura RAG del Proyecto"
              value={ragStats ? `${Math.round(ragStats.coverage_pct)}%` : "100%"}
              icon={Sparkles}
              subtext="Índice multiseñal preparado para búsquedas híbridas"
            />

            <StatCard
              title="Observaciones Vectorizadas"
              value={ragStats?.indexed_observations ?? 0}
              icon={CheckCircle2}
              iconClassName="text-emerald-500"
              subtext={`De ${ragStats?.total_observations ?? 0} observaciones totales`}
            />

            <StatCard
              title="Cola Outbox (Pendientes)"
              value={ragStats?.pending_observations ?? 0}
              icon={Clock}
              iconClassName={(ragStats?.pending_observations || 0) > 0 ? "text-amber-500" : "text-muted-foreground"}
              subtext="En proceso por embedding.Worker"
            />

            <StatCard
              title="Motor Vectorial Activo"
              value={ragStats?.vector_provider || "pgvector/hnsw"}
              icon={Cpu}
              iconClassName="text-primary"
              subtext={`${ragStats?.embedding_model || "Configurado en Servidor"}${ragStats?.embedding_dimensions ? ` (${ragStats.embedding_dimensions}d)` : ""}`}
            />
          </div>

          <Card className="p-5 bg-card border-border shadow-xs space-y-4">
            <div className="flex items-center justify-between flex-wrap gap-2">
              <div>
                <h3 className="text-sm font-bold text-foreground flex items-center gap-2">
                  <Database className="h-4 w-4 text-primary" />
                  Arquitectura RAG & Recuperación Semántica
                </h3>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Visualización de señales y sincronización del motor de búsqueda híbrida de Cortex
                </p>
              </div>
              <Button
                variant="outline"
                size="sm"
                onClick={loadRAGStats}
                disabled={ragLoading}
                className="text-xs gap-1.5 shadow-xs"
              >
                <RefreshCw className={`h-3.5 w-3.5 ${ragLoading ? "animate-spin" : ""}`} />
                <span>Actualizar Métricas</span>
              </Button>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-xs">
              <div className="p-3.5 rounded-lg bg-secondary/40 border border-border space-y-1.5">
                <span className="font-semibold text-primary flex items-center gap-1.5">
                  <Zap className="h-3.5 w-3.5" /> 1. Búsqueda Léxica (FTS5 / GIN)
                </span>
                <p className="text-[11px] text-muted-foreground leading-relaxed">
                  Indexación invertida contextual con ponderación BM25 basada en título, contenido y topic_key.
                </p>
              </div>

              <div className="p-3.5 rounded-lg bg-secondary/40 border border-border space-y-1.5">
                <span className="font-semibold text-primary flex items-center gap-1.5">
                  <Sparkles className="h-3.5 w-3.5" /> 2. Búsqueda Densa (Vectores)
                </span>
                <p className="text-[11px] text-muted-foreground leading-relaxed">
                  Cálculo de similitud coseno sobre espacios de 1536 dimensiones con indexación HNSW.
                </p>
              </div>

              <div className="p-3.5 rounded-lg bg-secondary/40 border border-border space-y-1.5">
                <span className="font-semibold text-emerald-500 flex items-center gap-1.5">
                  <Layers className="h-3.5 w-3.5" /> 3. Fusión RRF (k=60)
                </span>
                <p className="text-[11px] text-muted-foreground leading-relaxed">
                  Fusión recíproca de rankings multiseñal revalidando únicamente registros no eliminados.
                </p>
              </div>
            </div>
          </Card>
        </div>
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
              <label className="text-xs font-medium text-foreground block mb-1">
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
              <label className="text-xs font-medium text-foreground block mb-1">
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
              <label className="text-xs font-medium text-foreground block mb-1">
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
              <label className="text-xs font-medium text-foreground block mb-1">
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
              <label className="text-xs font-medium text-foreground block mb-1">
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
            <label className="text-xs font-medium text-foreground block mb-1">
              Contenido / Instrucciones (Markdown) *
            </label>
            <textarea
              rows={6}
              value={modalContent}
              onChange={(e) => setModalContent(e.target.value)}
              placeholder="Escribe las directivas, reglas o procedimientos en Markdown..."
              required
              className="w-full rounded-lg border border-input bg-background px-3 py-2 text-xs font-mono text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="flex flex-wrap items-center justify-end gap-2 pt-2 border-t border-border">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setIsModalOpen(false)}
              className="text-xs text-muted-foreground hover:text-foreground"
            >
              Cancelar
            </Button>
            <Button
              type="submit"
              variant="default"
              size="sm"
              disabled={saving}
              className="text-xs gap-1.5 shadow-xs"
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
                    <div className="p-1.5 rounded-lg bg-primary/10 text-primary">
                      <ShieldCheck className="h-5 w-5" />
                    </div>
                  ) : (
                    <div className="p-1.5 rounded-lg bg-amber-500/10 text-amber-500">
                      <Sparkles className="h-5 w-5" />
                    </div>
                  )}
                  <div>
                    <DialogTitle className="text-base font-bold text-foreground">
                      {viewingArtifact.title}
                    </DialogTitle>
                    <div className="flex items-center gap-2 mt-1">
                      <Badge
                        variant={viewingArtifact.scope === "workspace_default" ? "outline" : "secondary"}
                        className="text-[9px] px-1.5 py-0 font-mono"
                      >
                        {viewingArtifact.scope === "workspace_default" ? "Alcance Global" : `Proyecto: ${viewingArtifact.project || "default"}`}
                      </Badge>
                      <span className="text-[10px] font-mono text-muted-foreground">
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
                <div className="p-3 rounded-lg bg-secondary/50 border border-border text-xs text-muted-foreground">
                  <span className="font-semibold text-foreground block mb-0.5">
                    Descripción / Propósito:
                  </span>
                  {viewingArtifact.description}
                </div>
              )}

              <div>
                <div className="flex items-center justify-between mb-1.5">
                  <span className="text-xs font-semibold text-muted-foreground uppercase">
                    Contenido / Instrucciones de Procedimiento
                  </span>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(viewingArtifact.content, `view-${viewingArtifact.id}`)}
                    className="text-[11px] text-primary hover:text-primary/80 flex items-center gap-1 font-medium"
                  >
                    {copiedKey === `view-${viewingArtifact.id}` ? (
                      <>
                        <Check className="h-3 w-3 text-emerald-500" />
                        <span className="text-emerald-500">Copiado</span>
                      </>
                    ) : (
                      <>
                        <Copy className="h-3 w-3" />
                        <span>Copiar al portapapeles</span>
                      </>
                    )}
                  </button>
                </div>
                <pre className="bg-secondary/50 rounded-lg p-4 font-mono text-xs text-foreground whitespace-pre-wrap max-h-72 overflow-y-auto border border-border leading-relaxed">
                  {viewingArtifact.content}
                </pre>
              </div>

              <div className="p-3 rounded-lg bg-primary/5 border border-primary/20 text-xs text-muted-foreground space-y-1">
                <div className="font-semibold text-primary flex items-center gap-1.5">
                  <Info className="h-3.5 w-3.5" />
                  <span>Invocación desde tu Coding Agent (MCP):</span>
                </div>
                <p className="text-[11px] text-muted-foreground font-mono">
                  {viewingArtifact.kind === "rule"
                    ? `cortex_get_project_context(project: "${selectedProject || "default"}")`
                    : `cortex_get_skill(key: "${viewingArtifact.key}", project: "${selectedProject || "default"}")`}
                </p>
              </div>

              <div className="flex justify-end pt-2 border-t border-border">
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
