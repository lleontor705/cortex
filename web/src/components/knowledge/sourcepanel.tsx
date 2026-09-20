import type { AgentSource } from "../../lib/api";

export function SourcePanel({ sources }: { sources: AgentSource[] }) {
  return (
    <section className="rounded-lg border border-border bg-card p-4 shadow-sm" aria-labelledby="source-panel-title">
      <div className="flex items-center justify-between gap-3">
        <div>
          <p id="source-panel-title" className="text-xs font-semibold uppercase tracking-[0.18em] text-muted-foreground">Evidencia citada</p>
          <p className="mt-1 text-sm text-muted-foreground">Memoria y código usados para sustentar la respuesta.</p>
        </div>
        <span className="rounded-md font-mono bg-secondary px-2 py-0.5 text-xs text-muted-foreground border border-border">{sources.length}</span>
      </div>
      {sources.length ? (
        <div className="mt-4 grid gap-2">
          {sources.map((source) => {
            const lines = source.line_start ? `L${source.line_start}${source.line_end && source.line_end !== source.line_start ? `–${source.line_end}` : ""}` : "";
            return (
              <article key={source.handle} tabIndex={0} aria-label={`${source.type === "code" ? "Código" : "Memoria"}: ${source.title}`} className="rounded-lg border border-border bg-secondary/40 p-3 outline-none focus-visible:ring-2 focus-visible:ring-primary">
                <div className="flex items-start justify-between gap-3">
                  <p className="min-w-0 break-words text-sm font-medium text-foreground">{source.title}</p>
                  <span className="shrink-0 rounded-md font-mono border border-border bg-secondary px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">{source.type}</span>
                </div>
                {source.path ? <p className="mt-2 break-all font-mono text-xs text-muted-foreground">{source.path}{lines ? `:${lines}` : ""}</p> : null}
              </article>
            );
          })}
        </div>
      ) : <p className="mt-4 rounded-lg border border-dashed border-border p-4 text-sm text-muted-foreground" role="status">La respuesta aún no tiene evidencia citada.</p>}
    </section>
  );
}
