import Link from "next/link";
import { FolderKanban, Layers, Network, Database, Sparkles } from "lucide-react";
import type { AgentProject } from "../../lib/api";

type ScopeBarProps = {
  projects: AgentProject[];
  selectedProjectId: string;
  onProjectChange: (projectId: string) => void;
  disabled?: boolean;
};

export function ScopeBar({ projects, selectedProjectId, onProjectChange, disabled }: ScopeBarProps) {
  const selected = projects.find((project) => project.id === selectedProjectId);
  const status = selected
    ? `Consultando proyecto autorizado ${selected.label} con RAG multi-capa activo.`
    : projects.length
      ? "Selecciona un proyecto autorizado para comenzar."
      : "No hay proyectos autorizados disponibles.";

  return (
    <section className="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 shadow-sm" aria-labelledby="knowledge-scope-title">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl border border-blue-500/30 bg-blue-500/20 text-blue-400 font-bold text-sm">
            <FolderKanban className="h-4 w-4" />
          </span>
          <div>
            <p id="knowledge-scope-title" className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--text-muted)]">
              Alcance de conocimiento
            </p>
            <p className="mt-0.5 text-xs text-[var(--text-secondary)]">
              Tenant y workspace automáticos; selecciona el proyecto para RAG y contexto de directivas.
            </p>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2.5 sm:w-auto">
          <div className="flex items-center gap-2 rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 py-1.5 shadow-sm min-w-[240px]">
            <Layers className="h-4 w-4 shrink-0 text-blue-400" />
            <select
              aria-label="Proyecto autorizado"
              aria-describedby="knowledge-scope-status"
              className="w-full cursor-pointer bg-transparent text-xs font-semibold text-[var(--text-primary)] outline-none focus:ring-0 [&>option]:bg-[var(--bg-secondary)] [&>option]:text-[var(--text-primary)]"
              value={selectedProjectId}
              disabled={disabled || projects.length === 0}
              onChange={(event) => onProjectChange(event.target.value)}
            >
              <option value="">📁 Seleccionar Proyecto Autorizado</option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  📁 Proyecto: {project.label}
                </option>
              ))}
            </select>
          </div>

          {selected ? (
            <Link
              href={`/graph?project=${encodeURIComponent(selected.label)}`}
              className="flex h-9 items-center gap-1.5 rounded-xl border border-blue-500/40 bg-blue-600/10 px-3 text-xs font-medium text-blue-400 hover:bg-blue-600/20 transition-colors shadow-sm"
              title="Explorar Knowledge Graph"
            >
              <Network className="h-3.5 w-3.5" />
              <span>Grafo</span>
            </Link>
          ) : null}
        </div>
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-[var(--border-subtle)] pt-2.5">
        <p id="knowledge-scope-status" className="text-xs text-[var(--text-muted)]" role="status" aria-live="polite">
          {status}
        </p>
        {selected ? (
          <span className="inline-flex items-center gap-1 rounded-md border border-emerald-500/20 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-400">
            <Sparkles className="h-3 w-3" /> RAG Multi-Capa Activo
          </span>
        ) : null}
      </div>
    </section>
  );
}
