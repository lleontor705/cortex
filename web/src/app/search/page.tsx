"use client";

import React, { useEffect, useRef, useState } from "react";
import {
  AlertCircle,
  Braces,
  Code2,
  Database,
  FileSearch,
  Search,
  Sparkles,
} from "lucide-react";

import { ScopeBar } from "../../components/knowledge/scopebar";
import { Badge } from "../../components/ui/badge";
import { Button } from "../../components/ui/button";
import { Card } from "../../components/ui/card";
import { Input } from "../../components/ui/input";
import { useAuth } from "../../lib/auth-context";
import { APIError, type AgentProject, type CodeSymbol, type Observation } from "../../lib/api";

type SearchMode = "all" | "memory" | "code";
type BranchStatus = "idle" | "loading" | "ready" | "unavailable";

const modes: Array<{ value: SearchMode; label: string; description: string }> = [
  { value: "all", label: "Todo", description: "Memoria híbrida + AST" },
  { value: "memory", label: "Memoria", description: "Texto + vectores" },
  { value: "code", label: "Código", description: "Símbolos y archivos" },
];

function BranchMessage({ status, empty, unavailable }: { status: BranchStatus; empty: string; unavailable: string }) {
  if (status === "loading") {
    return <p className="rounded-xl border border-dashed border-[var(--border-subtle)] p-5 text-sm text-[var(--text-muted)]" role="status">Consultando este índice…</p>;
  }
  if (status === "unavailable") {
    return <p className="rounded-xl border border-amber-500/30 bg-amber-500/10 p-4 text-sm text-amber-400" role="status"><AlertCircle className="mr-2 inline h-4 w-4" aria-hidden="true" />{unavailable}</p>;
  }
  if (status === "ready") {
    return <p className="rounded-xl border border-dashed border-[var(--border-subtle)] p-5 text-sm text-[var(--text-muted)]" role="status">{empty}</p>;
  }
  return null;
}
function MemoryResults({ items, status, projectLabel }: { items: Observation[]; status: BranchStatus; projectLabel: string }) {
  return (
    <section className="min-w-0 space-y-3" aria-labelledby="memory-results-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="memory-results-title" className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
            <Database className="h-4 w-4 text-sky-400" aria-hidden="true" />
            Memoria híbrida
            <Badge variant="secondary">{items.length}</Badge>
          </h2>
          <p className="mt-1 text-xs text-[var(--text-muted)]">Coincidencia lexical y vectorial fusionada por el servidor.</p>
        </div>
        <Badge variant="outline">{projectLabel}</Badge>
      </div>
      {!items.length ? (
        <BranchMessage
          status={status}
          empty="El índice respondió correctamente, pero no encontró memorias para esta consulta."
          unavailable="El índice de memoria no está disponible. Los resultados de código, si existen, siguen siendo válidos."
        />
      ) : (
        <div className="grid min-w-0 gap-3">
          {items.map((item) => (
            <Card key={item.id} className="min-w-0 border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 sm:p-5">
              <div className="flex min-w-0 flex-wrap items-start justify-between gap-2">
                <h3 className="min-w-0 break-words text-sm font-semibold text-[var(--text-primary)]">{item.title}</h3>
                <div className="flex shrink-0 gap-2">
                  <Badge variant="secondary" className="text-[10px]">{item.type}</Badge>
                  {item.has_embedding ? <Badge variant="outline" className="text-[10px]">vector</Badge> : null}
                </div>
              </div>
              <p className="mt-3 whitespace-pre-wrap break-words text-sm leading-6 text-[var(--text-secondary)]">{item.content}</p>
              <p className="mt-3 border-t border-[var(--border-subtle)] pt-3 text-xs text-[var(--text-muted)]">
                Fuente: memoria · {item.project}
              </p>
            </Card>
          ))}
        </div>
      )}
    </section>
  );
}

function CodeResults({ items, status, projectLabel }: { items: CodeSymbol[]; status: BranchStatus; projectLabel: string }) {
  return (
    <section className="min-w-0 space-y-3" aria-labelledby="code-results-title">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 id="code-results-title" className="flex items-center gap-2 text-sm font-semibold text-[var(--text-primary)]">
            <Code2 className="h-4 w-4 text-violet-400" aria-hidden="true" />
            Índice estructural
            <Badge variant="secondary">{items.length}</Badge>
          </h2>
          <p className="mt-1 text-xs text-[var(--text-muted)]">Símbolos, firmas y rutas extraídos del AST.</p>
        </div>
        <Badge variant="outline">{projectLabel}</Badge>
      </div>
      {!items.length ? (
        <BranchMessage
          status={status}
          empty="El índice respondió correctamente, pero no encontró símbolos o archivos coincidentes."
          unavailable="El índice de código no está disponible o aún no fue generado para este proyecto."
        />
      ) : (
        <div className="grid min-w-0 gap-3 sm:grid-cols-2">
          {items.map((symbol) => (
            <Card key={symbol.id} tabIndex={0} className="min-w-0 border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)]">
              <div className="flex min-w-0 items-start justify-between gap-2">
                <h3 className="min-w-0 break-words text-sm font-semibold text-[var(--text-primary)]">{symbol.name}</h3>
                <Badge variant="purple" className="shrink-0 text-[10px]">{symbol.kind}</Badge>
              </div>
              <p className="mt-3 break-all font-mono text-xs text-[var(--text-secondary)]">{symbol.file_path}:{symbol.line_number}</p>
              {symbol.signature ? <p className="mt-3 break-words font-mono text-xs leading-5 text-[var(--text-muted)]">{symbol.signature}</p> : null}
              <p className="mt-3 border-t border-[var(--border-subtle)] pt-3 text-xs text-[var(--text-muted)]">Fuente: código · {projectLabel}</p>
            </Card>
          ))}
        </div>
      )}
    </section>
  );
}

export default function SearchPage() {
  const { client, principal } = useAuth();
  const [query, setQuery] = useState("");
  const [projectId, setProjectId] = useState("");
  const [projects, setProjects] = useState<AgentProject[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [projectsError, setProjectsError] = useState("");
  const [mode, setMode] = useState<SearchMode>("all");
  const [memories, setMemories] = useState<Observation[]>([]);
  const [symbols, setSymbols] = useState<CodeSymbol[]>([]);
  const [memoryStatus, setMemoryStatus] = useState<BranchStatus>("idle");
  const [codeStatus, setCodeStatus] = useState<BranchStatus>("idle");
  const [hasSearched, setHasSearched] = useState(false);
  const projectLoad = useRef<AbortController | null>(null);
  const activeSearch = useRef<AbortController | null>(null);
  const searchInput = useRef<HTMLInputElement | null>(null);

  const clearResults = () => {
    setMemories([]);
    setSymbols([]);
    setMemoryStatus("idle");
    setCodeStatus("idle");
    setHasSearched(false);
  };

  useEffect(() => {
    const controller = new AbortController();
    projectLoad.current?.abort();
    projectLoad.current = controller;
    activeSearch.current?.abort();
    setProjects([]);
    setProjectId("");
    clearResults();

    if (!client || !principal) {
      setProjectsLoading(false);
      return () => controller.abort();
    }

    setProjectsLoading(true);
    setProjectsError("");
    client.agentProjects(controller.signal)
      .then((eligible) => {
        if (controller.signal.aborted || projectLoad.current !== controller) return;
        setProjects(eligible);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted && projectLoad.current === controller) {
          setProjectsError(error instanceof Error ? error.message : "No se pudieron cargar los proyectos autorizados.");
        }
      })
      .finally(() => {
        if (!controller.signal.aborted && projectLoad.current === controller) setProjectsLoading(false);
      });

    return () => {
      controller.abort();
      if (projectLoad.current === controller) projectLoad.current = null;
      activeSearch.current?.abort();
    };
  }, [client, principal]);

  const selectedProject = projects.find((project) => project.id === projectId);

  const runSearch = async () => {
    const cleanQuery = query.trim();
    if (!client || !selectedProject || !cleanQuery) return;

    const controller = new AbortController();
    activeSearch.current?.abort();
    activeSearch.current = controller;
    setHasSearched(true);
    setMemories([]);
    setSymbols([]);
    setMemoryStatus(mode === "code" ? "idle" : "loading");
    setCodeStatus(mode === "memory" ? "idle" : "loading");

    const memoryPromise = mode === "code"
      ? Promise.resolve(null)
      : client.search(cleanQuery, selectedProject.label);
    const codePromise = mode === "memory"
      ? Promise.resolve(null)
      : client.getCodeSymbols({ project: selectedProject.label, q: cleanQuery, limit: 100 });
    const [memoryResult, codeResult] = await Promise.allSettled([memoryPromise, codePromise]);

    if (controller.signal.aborted || activeSearch.current !== controller) return;

    const failures = [memoryResult, codeResult].filter((result) => result.status === "rejected");
    const forbidden = failures.find((result) => result.status === "rejected" && result.reason instanceof APIError && result.reason.status === 403);
    if (forbidden) {
      setProjects((current) => current.filter((project) => project.id !== projectId));
      setProjectId("");
      setProjectsError("El proyecto dejó de estar autorizado. Selecciona otro alcance.");
      clearResults();
      activeSearch.current = null;
      return;
    }

    if (mode !== "code") {
      if (memoryResult.status === "fulfilled" && memoryResult.value) {
        setMemories(memoryResult.value.value);
        setMemoryStatus("ready");
      } else {
        setMemoryStatus("unavailable");
      }
    }
    if (mode !== "memory") {
      if (codeResult.status === "fulfilled" && codeResult.value) {
        setSymbols(codeResult.value);
        setCodeStatus("ready");
      } else {
        setCodeStatus("unavailable");
      }
    }
    activeSearch.current = null;
    queueMicrotask(() => searchInput.current?.focus());
  };

  const isSearching = memoryStatus === "loading" || codeStatus === "loading";
  const total = memories.length + symbols.length;
  const statusText = isSearching
    ? "Consultando índices autorizados"
    : hasSearched
      ? `${total} resultados; memoria ${memoryStatus}; código ${codeStatus}`
      : "Búsqueda lista";

  return (
    <main className="mx-auto min-w-0 max-w-[1400px] space-y-5 overflow-x-hidden pb-8">
      <header>
        <p className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-sky-400">
          <Sparkles className="h-4 w-4" aria-hidden="true" />
          Explorador de conocimiento
        </p>
        <h1 className="mt-2 text-2xl font-bold tracking-tight text-[var(--text-primary)] sm:text-3xl">Encuentra memoria y código</h1>
        <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--text-secondary)]">
          La memoria usa recuperación híbrida y vectorial; el código consulta símbolos AST. Cada rama muestra su disponibilidad por separado.
        </p>
      </header>

      <ScopeBar
        projects={projects}
        selectedProjectId={projectId}
        onProjectChange={(nextProject) => {
          activeSearch.current?.abort();
          activeSearch.current = null;
          setProjectId(nextProject);
          setProjectsError("");
          clearResults();
          queueMicrotask(() => searchInput.current?.focus());
        }}
        disabled={projectsLoading || isSearching}
      />

      {projectsError ? <p role="alert" className="rounded-xl border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-400">{projectsError}</p> : null}

      <Card className="min-w-0 space-y-4 border-[var(--border-subtle)] bg-[var(--bg-secondary)] p-4 sm:p-5">
        <div className="grid gap-2 sm:grid-cols-3" aria-label="Fuentes de búsqueda">
          {modes.map((item) => (
            <button
              key={item.value}
              type="button"
              aria-pressed={mode === item.value}
              className={`min-w-0 rounded-xl border p-3 text-left outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)] ${
                mode === item.value
                  ? "border-sky-500/50 bg-sky-500/10 text-[var(--text-primary)]"
                  : "border-[var(--border-subtle)] bg-[var(--bg-surface)] text-[var(--text-secondary)]"
              }`}
              onClick={() => {
                activeSearch.current?.abort();
                activeSearch.current = null;
                setMode(item.value);
                clearResults();
                queueMicrotask(() => searchInput.current?.focus());
              }}
            >
              <span className="block text-sm font-semibold">{item.label}</span>
              <span className="mt-1 block text-xs text-[var(--text-muted)]">{item.description}</span>
            </button>
          ))}
        </div>

        <form
          className="flex min-w-0 flex-col gap-3 sm:flex-row"
          onSubmit={(event) => {
            event.preventDefault();
            void runSearch();
          }}
        >
          <div className="relative min-w-0 flex-1">
            <Search className="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--text-muted)]" aria-hidden="true" />
            <Input
              ref={searchInput}
              type="search"
              aria-label="Consulta de conocimiento"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Ej.: ApplicationDbContext.cs, aislamiento tenant o decisión PostgreSQL"
              className="h-11 w-full min-w-0 bg-[var(--bg-surface)] pl-10 text-sm"
              disabled={!selectedProject || isSearching}
              required
            />
          </div>
          <Button type="submit" className="h-11 shrink-0 px-6" disabled={!selectedProject || !query.trim() || isSearching}>
            <FileSearch className="h-4 w-4" aria-hidden="true" />
            {isSearching ? "Buscando…" : "Buscar"}
          </Button>
        </form>
        <p className="sr-only" aria-live="polite" aria-atomic="true">{statusText}</p>
      </Card>

      {hasSearched ? (
        <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-[var(--text-muted)]">
          <p><strong className="text-[var(--text-primary)]">{total}</strong> resultados para “{query.trim()}”</p>
          <p>{selectedProject?.label}</p>
        </div>
      ) : null}

      <div className="grid min-w-0 gap-6">
        {mode !== "code" ? <MemoryResults items={memories} status={memoryStatus} projectLabel={selectedProject?.label ?? ""} /> : null}
        {mode !== "memory" ? <CodeResults items={symbols} status={codeStatus} projectLabel={selectedProject?.label ?? ""} /> : null}
      </div>

      {!hasSearched ? (
        <Card className="grid min-h-48 place-items-center border-dashed border-[var(--border-subtle)] bg-transparent p-6 text-center">
          <div>
            <Braces className="mx-auto h-6 w-6 text-[var(--text-muted)]" aria-hidden="true" />
            <p className="mt-3 text-sm font-medium text-[var(--text-primary)]">Selecciona un proyecto y formula una consulta</p>
            <p className="mt-1 text-xs text-[var(--text-muted)]">Los resultados nunca mezclan proyectos ni workspaces.</p>
          </div>
        </Card>
      ) : null}
    </main>
  );
}
