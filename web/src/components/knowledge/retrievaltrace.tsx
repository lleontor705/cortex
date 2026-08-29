import type { AgentConfidence, AgentRetrieval, AgentRetrievalStage } from "../../lib/api";

const tierLabels: Record<NonNullable<AgentRetrieval["tier"]>, string> = {
  direct_factual: "Respuesta directa",
  semantic_hybrid: "B�squeda h�brida",
  multi_hop_graph: "Grafo multi-salto",
  architectural_global: "S�ntesis arquitect�nica",
};

const stageLabels: Record<AgentRetrievalStage["name"], string> = {
  lexical: "Texto exacto",
  dense: "Vectores sem�nticos",
  rrf_maxsim: "Fusi�n y reordenamiento",
  graph_ppr: "PageRank del grafo",
  community_summary: "Resumen de comunidades",
  code: "S�mbolos y AST",
  crag: "Refinamiento CRAG",
};

const statusLabels: Record<AgentRetrievalStage["status"], string> = {
  ok: "Completado",
  degraded: "Parcial",
  skipped: "Omitido",
};

function readable(value: string): string {
  return value.replaceAll("_", " ");
}

export function RetrievalTrace({ retrieval, confidence }: { retrieval?: AgentRetrieval; confidence?: AgentConfidence }) {
  if (!retrieval && !confidence) return null;
  const summary = retrieval?.tier ? tierLabels[retrieval.tier] : "Recuperaci�n disponible";

  return (
    <section className="rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] p-4" aria-labelledby="retrieval-trace-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p id="retrieval-trace-title" className="text-xs font-semibold uppercase tracking-[0.18em] text-[var(--text-muted)]">C�mo se obtuvo</p>
          <p className="mt-1 text-sm font-medium text-[var(--text-primary)]">{summary}</p>
        </div>
        {confidence ? <span className="rounded-full bg-[var(--bg-elevated)] px-3 py-1 text-xs text-[var(--text-secondary)]">Confianza {confidence.level} � {Math.round(confidence.score * 100)}%</span> : null}
      </div>
      {retrieval?.stages?.length ? (
        <ol className="mt-4 grid gap-2 sm:grid-cols-2">
          {retrieval.stages.map((stage) => (
            <li key={stage.name} className="flex items-center justify-between gap-3 rounded-xl border border-[var(--border-subtle)] px-3 py-2 text-xs">
              <span className="font-medium text-[var(--text-primary)]">{stageLabels[stage.name]}</span>
              <span className="text-right text-[var(--text-muted)]">{statusLabels[stage.status]} � {stage.count}</span>
            </li>
          ))}
        </ol>
      ) : null}
      {retrieval?.degraded.length ? (
        <div className="mt-3 flex flex-wrap gap-2" role="status" aria-live="polite">
          <span className="sr-only">Recuperaci�n parcial:</span>
          {retrieval.degraded.map((reason) => <span key={reason} className="rounded-full bg-amber-500/10 px-2.5 py-1 text-xs text-amber-400">{readable(reason)}</span>)}
        </div>
      ) : <p className="sr-only" role="status" aria-live="polite">Recuperaci�n completada sin degradaciones.</p>}
    </section>
  );
}
