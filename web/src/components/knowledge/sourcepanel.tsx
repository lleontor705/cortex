import type { AgentSource } from "../../lib/api";

export function SourcePanel({ sources }: { sources: AgentSource[] }) {
  return (
    <section className="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-4" aria-labelledby="source-panel-title">
      <div className="flex items-center justify-between gap-3">
        <div>
          <p id="source-panel-title" className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--text-muted)]">Evidencia citada</p>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">Memoria y código usados para sustentar la respuesta.</p>
        </div>
        <span className="rounded-full bg-[var(--bg-elevated)] px-2.5 py-1 text-xs text-[var(--text-muted)]">{sources.length}</span>
      </div>
      {sources.length ? (
        <div className="mt-4 grid gap-2">
          {sources.map((source) => {
            const lines = source.line_start ? `L${source.line_start}${source.line_end && source.line_end !== source.line_start ? `–${source.line_end}` : ""}` : "";
            return (
              <article key={source.handle} tabIndex={0} aria-label={`${source.type === "code" ? "Código" : "Memoria"}: ${source.title}`} className="rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-elevated)] p-3 outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)]">
                <div className="flex items-start justify-between gap-3">
                  <p className="min-w-0 break-words text-sm font-medium text-[var(--text-primary)]">{source.title}</p>
                  <span className="shrink-0 rounded-full border border-[var(--border-subtle)] px-2 py-0.5 text-[10px] uppercase text-[var(--text-muted)]">{source.type}</span>
                </div>
                {source.path ? <p className="mt-2 break-all font-mono text-xs text-[var(--text-muted)]">{source.path}{lines ? `:${lines}` : ""}</p> : null}
              </article>
            );
          })}
        </div>
      ) : <p className="mt-4 rounded-xl border border-dashed border-[var(--border-subtle)] p-4 text-sm text-[var(--text-muted)]" role="status">La respuesta aún no tiene evidencia citada.</p>}
    </section>
  );
}
