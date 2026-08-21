"use client";

import dynamic from "next/dynamic";
import { Suspense } from "react";

const GraphView = dynamic(() => import("@/components/graph-view"), {
  ssr: false,
  loading: () => (
    <div className="flex h-[60vh] items-center justify-center text-xs text-[var(--text-muted)]">
      <div className="flex items-center gap-2">
        <span className="h-4 w-4 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
        <span>Cargando Grafo Sigma.js...</span>
      </div>
    </div>
  ),
});

export default function GraphPage() {
  return (
    <Suspense
      fallback={
        <div className="flex h-[60vh] items-center justify-center text-xs text-[var(--text-muted)]">
          <div className="flex items-center gap-2">
            <span className="h-4 w-4 animate-spin rounded-full border-2 border-blue-500 border-t-transparent" />
            <span>Cargando Grafo Sigma.js...</span>
          </div>
        </div>
      }
    >
      <GraphView />
    </Suspense>
  );
}
