"use client";

import React, { useCallback, useEffect, useReducer, useRef, useState } from "react";
import {
  Bot,
  CircleStop,
  Download,
  MessageCircleQuestion,
  Plus,
  RotateCcw,
  Send,
  ShieldCheck,
  Sparkles,
} from "lucide-react";

import { ScopeBar } from "../../components/knowledge/scopebar";
import { RetrievalTrace } from "../../components/knowledge/retrievaltrace";
import { SourcePanel } from "../../components/knowledge/sourcepanel";
import { Button } from "../../components/ui/button";
import { useAuth } from "../../lib/auth-context";
import type {
  AgentAnswer,
  AgentConfidence,
  AgentProject,
  AgentRetrieval,
  AgentSource,
  AgentStreamEvent,
} from "../../lib/api";
import { APIError } from "../../lib/api";
import {
  agentChatReducer,
  historyForAgentRequest,
  initialAgentChatState,
  type AgentChatMessage,
} from "../../lib/agent-chat";

type AgentPageViewProps = {
  projects: AgentProject[];
  projectsLoading: boolean;
  projectsError: string;
  projectId: string;
  messages: AgentChatMessage[];
  question: string;
  pendingQuestion: string;
  draftAnswer: string;
  sources: AgentSource[];
  confidence?: AgentConfidence;
  retrieval?: AgentRetrieval;
  status: "idle" | "loading" | "streaming" | "error";
  error: string;
  composerRef: React.RefObject<HTMLTextAreaElement | null>;
  onProjectChange: (projectId: string) => void;
  onQuestionChange: (question: string) => void;
  onSubmit: (event: React.FormEvent<HTMLFormElement>) => void;
  onStop: () => void;
  onRetry: () => void;
  onNewConversation: () => void;
};

function AnswerEvidence({ message }: { message: AgentChatMessage }) {
  if (message.role !== "assistant") return null;
  return (
    <details className="mt-4 border-t border-[var(--border-subtle)] pt-3">
      <summary className="cursor-pointer text-xs font-semibold text-[var(--accent-primary)]">
        Ver trazabilidad y evidencia
      </summary>
      <div className="mt-3 space-y-3">
        <RetrievalTrace confidence={message.confidence} retrieval={message.retrieval} />
        <SourcePanel sources={message.sources ?? []} />
      </div>
    </details>
  );
}

function ConversationMessage({ message, index }: { message: AgentChatMessage; index: number }) {
  const assistant = message.role === "assistant";
  return (
    <div className={`flex min-w-0 gap-3 ${assistant ? "justify-start" : "justify-end"}`}>
      {assistant ? (
        <span className="mt-1 grid h-8 w-8 shrink-0 place-items-center rounded-xl bg-sky-500/10 text-sky-400">
          <Bot className="h-4 w-4" aria-hidden="true" />
        </span>
      ) : null}
      <article
        aria-label={assistant ? `Respuesta ${index + 1}` : `Pregunta ${index + 1}`}
        className={`min-w-0 max-w-[92%] rounded-2xl px-4 py-3 text-sm leading-6 sm:max-w-[82%] ${
          assistant
            ? "border border-[var(--border-subtle)] bg-[var(--bg-surface)] text-[var(--text-primary)]"
            : "bg-[var(--accent-primary)] text-white"
        }`}
      >
        <p className="whitespace-pre-wrap break-words">{message.content}</p>
        <AnswerEvidence message={message} />
      </article>
    </div>
  );
}

function AgentPageView(props: AgentPageViewProps) {
  const busy = props.status === "loading" || props.status === "streaming";
  const selectedIsGranted = props.projects.some((project) => project.id === props.projectId);
  const canAsk = selectedIsGranted && props.question.trim().length > 0 && !busy;
  const statusText = props.projectsLoading
    ? "Cargando proyectos autorizados"
    : props.status === "loading"
      ? "Recuperando memoria, código y grafo autorizados"
      : props.status === "streaming"
        ? "Generando una respuesta con evidencia"
        : props.status === "error"
          ? "La respuesta no pudo completarse"
          : "Agente listo";

  return (
    <main className="mx-auto min-w-0 max-w-[1500px] space-y-4 overflow-x-hidden pb-6">
      <header className="flex min-w-0 flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="min-w-0">
          <p className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.2em] text-sky-400">
            <Sparkles className="h-4 w-4" aria-hidden="true" />
            Conocimiento del proyecto
          </p>
          <h1 className="mt-2 text-2xl font-bold tracking-tight text-[var(--text-primary)] sm:text-3xl">
            Pregunta, comprende, decide
          </h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--text-secondary)]">
            El agente combina búsqueda híbrida, vectores, AST y grafos únicamente dentro del proyecto que tienes autorizado.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {props.messages.length > 0 ? (
            <Button
              type="button"
              variant="outline"
              onClick={() => {
                const projectName = props.projects.find((p) => p.id === props.projectId)?.label || props.projectId;
                let md = `# Conversación de Investigación - Proyecto: ${projectName}\n\n`;
                props.messages.forEach((m) => {
                  if (m.role === "user") {
                    md += `### 👤 Pregunta\n${m.content}\n\n`;
                  } else {
                    md += `### 🤖 Respuesta del Agente\n${m.content}\n\n`;
                    if (m.sources && m.sources.length > 0) {
                      md += `**Evidencia Citada:**\n`;
                      m.sources.forEach((s) => {
                        md += `- [${s.type.toUpperCase()}] **${s.title}** (\`${s.handle}\`)${s.path ? ` - \`${s.path}\`` : ""}\n`;
                      });
                      md += `\n`;
                    }
                  }
                });
                const blob = new Blob([md], { type: "text/markdown;charset=utf-8;" });
                const url = URL.createObjectURL(blob);
                const link = document.createElement("a");
                link.href = url;
                link.setAttribute("download", `cortex-investigacion-${projectName}-${new Date().toISOString().slice(0, 10)}.md`);
                document.body.appendChild(link);
                link.click();
                document.body.removeChild(link);
              }}
              title="Exportar conversación a Markdown"
            >
              <Download className="h-4 w-4" aria-hidden="true" />
              Exportar
            </Button>
          ) : null}
          <Button
            type="button"
            variant="outline"
            onClick={props.onNewConversation}
            disabled={busy || (!props.messages.length && !props.draftAnswer && !props.error)}
          >
            <Plus className="h-4 w-4" aria-hidden="true" />
            Nueva conversación
          </Button>
        </div>
      </header>

      <ScopeBar
        projects={props.projects}
        selectedProjectId={props.projectId}
        onProjectChange={props.onProjectChange}
        disabled={props.projectsLoading || busy}
      />

      {props.projectsError ? (
        <p className="rounded-xl border border-red-500/30 bg-red-500/10 p-3 text-sm text-red-400" role="alert">
          {props.projectsError}
        </p>
      ) : null}

      <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <section className="flex min-h-[34rem] min-w-0 flex-col overflow-hidden rounded-2xl border border-[var(--border-subtle)] bg-[var(--bg-secondary)]" aria-label="Conversación del proyecto">
          <div className="flex-1 space-y-5 overflow-y-auto p-3 sm:p-5">
            {!props.messages.length && !props.pendingQuestion && !props.error ? (
              <div className="grid min-h-72 place-items-center px-3 text-center">
                <div>
                  <span className="mx-auto grid h-14 w-14 place-items-center rounded-2xl border border-sky-500/25 bg-sky-500/10 text-sky-400">
                    <MessageCircleQuestion className="h-6 w-6" aria-hidden="true" />
                  </span>
                  <h2 className="mt-4 text-base font-semibold text-[var(--text-primary)]">Explora lo que Cortex ya sabe</h2>
                  <p className="mx-auto mt-2 max-w-md text-sm leading-6 text-[var(--text-muted)]">
                    Pregunta por decisiones, dependencias, implementaciones, riesgos o archivos como ApplicationDbContext.cs.
                  </p>
                </div>
              </div>
            ) : null}

            {props.messages.map((message, index) => (
              <ConversationMessage key={`${message.role}-${index}`} message={message} index={index} />
            ))}

            {props.pendingQuestion ? (
              <div className="flex justify-end">
                <div className="max-w-[92%] rounded-2xl bg-[var(--accent-primary)] px-4 py-3 text-sm leading-6 text-white sm:max-w-[82%]">
                  <p className="whitespace-pre-wrap break-words">{props.pendingQuestion}</p>
                </div>
              </div>
            ) : null}

            {props.draftAnswer || busy ? (
              <div className="flex min-w-0 gap-3">
                <span className="mt-1 grid h-8 w-8 shrink-0 place-items-center rounded-xl bg-sky-500/10 text-sky-400">
                  <Bot className="h-4 w-4" aria-hidden="true" />
                </span>
                <div className="min-w-0 max-w-[92%] rounded-2xl border border-sky-500/25 bg-sky-500/5 px-4 py-3 text-sm leading-6 sm:max-w-[82%]">
                  <p className="whitespace-pre-wrap break-words">{props.draftAnswer || "Recuperando evidencia autorizada…"}</p>
                </div>
              </div>
            ) : null}

            {props.error ? (
              <div className="rounded-xl border border-red-500/30 bg-red-500/10 p-4 text-sm text-red-400" role="alert">
                <p>{props.error}</p>
                <Button type="button" variant="ghost" size="sm" className="mt-2 text-red-300" onClick={props.onRetry}>
                  <RotateCcw className="h-4 w-4" aria-hidden="true" />
                  Reintentar
                </Button>
              </div>
            ) : null}
          </div>

          <form className="sticky bottom-0 border-t border-[var(--border-subtle)] bg-[var(--bg-secondary)]/95 p-3 backdrop-blur sm:p-4" onSubmit={props.onSubmit}>
            <label htmlFor="agent-question" className="sr-only">Pregunta sobre el proyecto autorizado</label>
            <textarea
              ref={props.composerRef}
              id="agent-question"
              value={props.question}
              onChange={(event) => props.onQuestionChange(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  event.currentTarget.form?.requestSubmit();
                }
              }}
              disabled={!selectedIsGranted || busy}
              maxLength={8192}
              rows={3}
              placeholder={selectedIsGranted ? "Pregunta por una decisión, símbolo, archivo o dependencia…" : "Selecciona un proyecto autorizado"}
              className="w-full min-w-0 resize-none rounded-xl border border-[var(--border-subtle)] bg-[var(--bg-surface)] px-3 py-3 text-sm text-[var(--text-primary)] outline-none placeholder:text-[var(--text-muted)] focus-visible:ring-2 focus-visible:ring-[var(--accent-primary)] disabled:opacity-60"
            />
            <div className="mt-3 flex flex-wrap items-center justify-between gap-3">
              <p className="text-xs text-[var(--text-muted)]">Enter envía · Shift+Enter crea una línea</p>
              {busy ? (
                <Button type="button" variant="destructive" onClick={props.onStop}>
                  <CircleStop className="h-4 w-4" aria-hidden="true" />
                  Detener
                </Button>
              ) : (
                <Button type="submit" disabled={!canAsk}>
                  <Send className="h-4 w-4" aria-hidden="true" />
                  Preguntar
                </Button>
              )}
            </div>
            <p className="sr-only" aria-live="polite" aria-atomic="true">{statusText}</p>
          </form>
        </section>

        <aside className="min-w-0 space-y-3 lg:sticky lg:top-4 lg:self-start" aria-label="Trazabilidad de la respuesta actual">
          <div className="flex items-center gap-2 rounded-xl border border-emerald-500/20 bg-emerald-500/5 px-3 py-2 text-xs text-emerald-400">
            <ShieldCheck className="h-4 w-4 shrink-0" aria-hidden="true" />
            Evidencia limitada al alcance autorizado
          </div>
          <RetrievalTrace confidence={props.confidence} retrieval={props.retrieval} />
          <SourcePanel sources={props.sources} />
        </aside>
      </div>
    </main>
  );
}

export default function AgentPage() {
  const { client, principal } = useAuth();
  const [state, dispatch] = useReducer(agentChatReducer, initialAgentChatState);
  const [projects, setProjects] = useState<AgentProject[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [projectsError, setProjectsError] = useState("");
  const [question, setQuestion] = useState("");
  const activeRequest = useRef<AbortController | null>(null);
  const projectLoad = useRef<AbortController | null>(null);
  const composerRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    projectLoad.current?.abort();
    projectLoad.current = controller;
    activeRequest.current?.abort();
    dispatch({ type: "logout" });
    setProjects([]);
    setProjectsError("");

    if (!client || !principal) {
      setProjectsLoading(false);
      return () => {
        controller.abort();
        if (projectLoad.current === controller) projectLoad.current = null;
      };
    }

    setProjectsLoading(true);
    client.agentProjects(controller.signal)
      .then((eligible) => {
        if (controller.signal.aborted || projectLoad.current !== controller) return;
        setProjects(eligible);
        dispatch({ type: "sync_projects", projectIds: eligible.map((project) => project.id) });
        if (eligible.length > 0) {
          dispatch({ type: "select_project", projectId: eligible[0].id });
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setProjectsError(error instanceof Error ? error.message : "No se pudieron cargar los proyectos.");
      })
      .finally(() => {
        if (!controller.signal.aborted && projectLoad.current === controller) setProjectsLoading(false);
      });

    return () => {
      controller.abort();
      if (projectLoad.current === controller) projectLoad.current = null;
      activeRequest.current?.abort();
    };
  }, [client, principal]);

  const runQuestion = useCallback(async (nextQuestion: string, isRetry = false) => {
    if (!client || !projects.some((project) => project.id === state.projectId)) {
      dispatch({ type: "select_project", projectId: "" });
      return;
    }

    const controller = new AbortController();
    const requestId = crypto.randomUUID();
    activeRequest.current?.abort();
    activeRequest.current = controller;
    dispatch(isRetry ? { type: "retry", requestId } : { type: "ask", question: nextQuestion, requestId });

    const onEvent = (event: AgentStreamEvent) => {
      switch (event.type) {
        case "meta":
          dispatch({ type: "meta", requestId, confidence: event.data.confidence, retrieval: event.data.retrieval });
          break;
        case "delta":
          dispatch({ type: "delta", requestId, text: event.data.text });
          break;
        case "sources":
          event.data.forEach((source) => dispatch({ type: "citation", requestId, source }));
          break;
        case "done":
          break;
      }
    };

    try {
      const answer: AgentAnswer = await client.streamAgent({
        project_id: state.projectId,
        question: nextQuestion,
        history: historyForAgentRequest(state),
      }, onEvent, controller.signal);
      if (!controller.signal.aborted) dispatch({ type: "complete", requestId, answer });
    } catch (error: unknown) {
      if (!controller.signal.aborted) {
        if (error instanceof APIError && error.status === 403) {
          setProjects((current) => current.filter((project) => project.id !== state.projectId));
          setProjectsError("El proyecto dejó de estar autorizado. Selecciona otro alcance.");
          setQuestion("");
          dispatch({ type: "select_project", projectId: "" });
        } else {
          dispatch({ type: "error", requestId, message: error instanceof Error ? error.message : "No se pudo completar la respuesta." });
        }
      }
    } finally {
      if (activeRequest.current === controller) {
        activeRequest.current = null;
        queueMicrotask(() => composerRef.current?.focus());
      }
    }
  }, [client, projects, state]);

  const handleSubmit = (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const nextQuestion = question.trim();
    if (!nextQuestion) return;
    setQuestion("");
    void runQuestion(nextQuestion);
  };

  const handleStop = () => {
    activeRequest.current?.abort();
    activeRequest.current = null;
    dispatch({ type: "stop" });
    composerRef.current?.focus();
  };

  const handleNewConversation = () => {
    activeRequest.current?.abort();
    activeRequest.current = null;
    setQuestion("");
    dispatch({ type: "new_conversation" });
    composerRef.current?.focus();
  };

  return (
    <AgentPageView
      projects={projects}
      projectsLoading={projectsLoading}
      projectsError={projectsError}
      projectId={state.projectId}
      messages={state.messages}
      question={question}
      pendingQuestion={state.pendingQuestion}
      draftAnswer={state.draftAnswer}
      sources={state.sources}
      confidence={state.confidence}
      retrieval={state.retrieval}
      status={state.status}
      error={state.error}
      composerRef={composerRef}
      onProjectChange={(projectId) => {
        activeRequest.current?.abort();
        activeRequest.current = null;
        setQuestion("");
        setProjectsError("");
        dispatch({ type: "select_project", projectId });
        queueMicrotask(() => composerRef.current?.focus());
      }}
      onQuestionChange={setQuestion}
      onSubmit={handleSubmit}
      onStop={handleStop}
      onRetry={() => {
        if (state.lastQuestion) void runQuestion(state.lastQuestion, true);
      }}
      onNewConversation={handleNewConversation}
    />
  );
}
