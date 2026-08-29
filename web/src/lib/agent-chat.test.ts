import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  agentChatReducer,
  historyForAgentRequest,
  initialAgentChatState,
} from "./agent-chat";

describe("agentChatReducer", () => {
  it("keeps only the latest six complete turn pairs", () => {
    let state = { ...initialAgentChatState, projectId: "project-1" };
    for (let index = 1; index <= 7; index += 1) {
      const requestId = `request-${index}`;
      state = agentChatReducer(state, { type: "ask", question: `q${index}`, requestId });
      state = agentChatReducer(state, {
        type: "complete",
        requestId,
        answer: {
          answer: `a${index}`,
          sources: [],
          confidence: { level: "medium", score: 0.6 },
          retrieval: { degraded: [] },
        },
      });
    }

    expect(state.messages).toHaveLength(12);
    expect(state.messages[0]).toMatchObject({ role: "user", content: "q2" });
    expect(state.messages.at(-1)).toMatchObject({ role: "assistant", content: "a7" });
    expect(historyForAgentRequest(state)).toEqual(
      state.messages.map(({ role, content }) => ({ role, content })),
    );
  });

  it("builds a progressive response but only commits it when done", () => {
    let state = agentChatReducer(
      { ...initialAgentChatState, projectId: "project-1" },
      { type: "ask", question: "q", requestId: "request-progress" },
    );
    state = agentChatReducer(state, { type: "delta", requestId: "request-progress", text: "Usa " });
    state = agentChatReducer(state, { type: "delta", requestId: "request-progress", text: "PostgreSQL." });

    expect(state.draftAnswer).toBe("Usa PostgreSQL.");
    expect(historyForAgentRequest(state)).toEqual([]);

    state = agentChatReducer(state, {
      type: "complete",
      requestId: "request-progress",
      answer: {
        answer: "Usa PostgreSQL.",
        sources: [],
        confidence: { level: "high", score: 0.9 },
        retrieval: { degraded: [] },
      },
    });
    expect(historyForAgentRequest(state)).toEqual([
      { role: "user", content: "q" },
      { role: "assistant", content: "Usa PostgreSQL." },
    ]);
  });

  it("Stop removes the pending turn while logout clears all ephemeral state", () => {
    let state = agentChatReducer(
      { ...initialAgentChatState, projectId: "project-1" },
      { type: "ask", question: "pending", requestId: "request-stop" },
    );
    state = agentChatReducer(state, { type: "delta", requestId: "request-stop", text: "partial" });
    state = agentChatReducer(state, { type: "stop" });

    expect(state.messages).toEqual([]);
    expect(state.draftAnswer).toBe("");
    expect(state.status).toBe("idle");

    state = agentChatReducer(state, { type: "ask", question: "q", requestId: "request-after-stop" });
    state = agentChatReducer(state, {
      type: "complete",
      requestId: "request-after-stop",
      answer: {
        answer: "a",
        sources: [],
        confidence: { level: "low", score: 0.2 },
        retrieval: { degraded: ["code_unavailable"] },
      },
    });
    state = agentChatReducer(state, { type: "logout" });
    expect(state).toEqual(initialAgentChatState);
  });

  it("clears conversation when the granted project changes or disappears", () => {
    let state = agentChatReducer(
      { ...initialAgentChatState, projectId: "project-1" },
      { type: "ask", question: "q", requestId: "request-project" },
    );
    state = agentChatReducer(state, {
      type: "complete",
      requestId: "request-project",
      answer: {
        answer: "a",
        sources: [],
        confidence: { level: "low", score: 0.2 },
        retrieval: { degraded: [] },
      },
    });

    state = agentChatReducer(state, { type: "select_project", projectId: "project-2" });
    expect(state.projectId).toBe("project-2");
    expect(state.messages).toEqual([]);

    state = agentChatReducer(state, { type: "select_project", projectId: "" });
    expect(state).toEqual(initialAgentChatState);
  });

  it("clears a stale project when refreshed grants no longer include it", () => {
    const withConversation = {
      ...initialAgentChatState,
      projectId: "project-removed",
      messages: [{ role: "user" as const, content: "private context" }],
    };

    expect(agentChatReducer(withConversation, {
      type: "sync_projects",
      projectIds: ["project-allowed"],
    })).toEqual(initialAgentChatState);
    expect(agentChatReducer(
      { ...initialAgentChatState, projectId: "project-allowed" },
      { type: "sync_projects", projectIds: ["project-allowed"] },
    ).projectId).toBe("project-allowed");
  });

  it("rejects late metadata and citations after a project grant is revoked", () => {
    let state = agentChatReducer(
      { ...initialAgentChatState, projectId: "project-a" },
      { type: "ask", question: "private", requestId: "request-a" },
    );
    state = agentChatReducer(state, { type: "sync_projects", projectIds: [] });
    const revoked = state;
    state = agentChatReducer(state, {
      type: "meta",
      requestId: "request-a",
      retrieval: { degraded: ["code_unavailable"] },
    });
    state = agentChatReducer(state, {
      type: "citation",
      requestId: "request-a",
      source: { handle: "src_private", type: "code", title: "Private source" },
    });

    expect(state).toEqual(revoked);
    expect(state.sources).toEqual([]);
    expect(state.retrieval).toBeUndefined();
  });

  it("rejects every late callback from request A while request B is active", () => {
    let state = agentChatReducer(
      { ...initialAgentChatState, projectId: "project-a" },
      { type: "ask", question: "A", requestId: "request-a" },
    );
    state = agentChatReducer(state, { type: "stop" });
    state = agentChatReducer(state, { type: "select_project", projectId: "project-b" });
    state = agentChatReducer(state, { type: "ask", question: "B", requestId: "request-b" });
    const requestB = state;
    state = agentChatReducer(state, { type: "delta", requestId: "request-a", text: "private A" });
    state = agentChatReducer(state, {
      type: "meta",
      requestId: "request-a",
      retrieval: { degraded: ["memory_unavailable"] },
    });
    state = agentChatReducer(state, {
      type: "citation",
      requestId: "request-a",
      source: { handle: "src_a", type: "memory", title: "A" },
    });
    state = agentChatReducer(state, {
      type: "complete",
      requestId: "request-a",
      answer: {
        answer: "A",
        sources: [],
        confidence: { level: "high", score: 1 },
        retrieval: { degraded: [] },
      },
    });
    state = agentChatReducer(state, { type: "error", requestId: "request-a", message: "late" });

    expect(state).toEqual(requestB);
    expect(state.activeRequestId).toBe("request-b");
    expect(state.pendingQuestion).toBe("B");
  });
});

describe("knowledge transparency components", () => {
  it("declares authorized scope, trace and evidence accessibility contracts", () => {
    const component = (name: string) => readFileSync(resolve(process.cwd(), `src/components/knowledge/${name}.tsx`), "utf8");
    const scope = component("scopebar");
    const trace = component("retrievaltrace");
    const sources = component("sourcepanel");

    expect(scope).toContain("Proyecto autorizado");
    expect(scope).toContain('aria-live="polite"');
    expect(trace).toContain("Grafo multi-salto");
    expect(trace).toContain("PageRank del grafo");
    expect(trace).toContain('role="status"');
    expect(sources).toContain("Evidencia citada");
    expect(sources).toContain("tabIndex={0}");
  });
});
