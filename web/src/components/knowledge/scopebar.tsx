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
    ? `Consultando solo el proyecto autorizado ${selected.label}.`
    : projects.length
      ? "Selecciona un proyecto autorizado para comenzar."
      : "No hay proyectos autorizados disponibles.";

  return (
    <section className="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-4" aria-labelledby="knowledge-scope-title">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <p id="knowledge-scope-title" className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--text-muted)]">Alcance de conocimiento</p>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">Tenant y workspace se derivan de tu sesi�n; aqu� solo eliges entre proyectos concedidos.</p>
        </div>
        <label className="min-w-0 sm:w-72">
          <span className="sr-only">Proyecto autorizado</span>
          <select
            aria-label="Proyecto autorizado"
            aria-describedby="knowledge-scope-status"
            className="h-11 w-full rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] px-3 text-sm text-[var(--text-primary)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)]"
            value={selectedProjectId}
            disabled={disabled || projects.length === 0}
            onChange={(event) => onProjectChange(event.target.value)}
          >
            <option value="">Seleccionar proyecto</option>
            {projects.map((project) => <option key={project.id} value={project.id}>{project.label}</option>)}
          </select>
        </label>
      </div>
      <p id="knowledge-scope-status" className="mt-3 text-xs text-[var(--text-muted)]" role="status" aria-live="polite">{status}</p>
    </section>
  );
}
