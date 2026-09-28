"use client";

const statCardCount = 4;
const listRowCount = 3;

export default function Loading() {
  return (
    <div
      className="space-y-6"
      role="status"
      aria-live="polite"
      aria-busy="true"
      aria-label="Cargando contenido"
    >
      <div className="flex flex-wrap items-center justify-between gap-4 border-b border-border/40 pb-2">
        <div className="space-y-2">
          <div className="h-7 w-56 rounded-md bg-secondary/70 animate-pulse" />
          <div className="h-3 w-80 max-w-full rounded bg-secondary/50 animate-pulse" />
        </div>
        <div className="h-8 w-32 rounded-lg bg-secondary/70 animate-pulse" />
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: statCardCount }, (_, index) => (
          <div key={index} className="h-[104px] rounded-lg border border-border bg-card p-5">
            <div className="h-3 w-24 rounded bg-secondary/60 animate-pulse" />
            <div className="mt-3 h-7 w-16 rounded bg-secondary/70 animate-pulse" />
            <div className="mt-3 h-3 w-32 rounded bg-secondary/40 animate-pulse" />
          </div>
        ))}
      </div>

      <div className="rounded-lg border border-border bg-card">
        <div className="border-b border-border p-5">
          <div className="h-4 w-40 rounded bg-secondary/70 animate-pulse" />
        </div>
        <div className="divide-y divide-border">
          {Array.from({ length: listRowCount }, (_, index) => (
            <div key={index} className="flex items-center gap-4 p-5">
              <div className="h-9 w-9 shrink-0 rounded-lg bg-secondary/70 animate-pulse" />
              <div className="min-w-0 flex-1 space-y-2">
                <div className="h-3 w-2/5 rounded bg-secondary/60 animate-pulse" />
                <div className="h-3 w-3/5 rounded bg-secondary/40 animate-pulse" />
              </div>
              <div className="h-6 w-16 shrink-0 rounded-md bg-secondary/60 animate-pulse" />
            </div>
          ))}
        </div>
      </div>

      <span className="sr-only">Cargando…</span>
    </div>
  );
}
