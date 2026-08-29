import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const pageSource = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");
const globalStyles = readFileSync(new URL("../globals.css", import.meta.url), "utf8");

describe("/agent page contract", () => {
  it("keeps the authorized scope visible and uses the shared knowledge inspector", () => {
    expect(pageSource).toContain(".agentProjects(controller.signal)");
    const loadThen = pageSource.indexOf(".then((eligible)");
    const staleLoadGuard = pageSource.indexOf("if (controller.signal.aborted || projectLoad.current !== controller) return;");
    const publishProjects = pageSource.indexOf("setProjects(eligible)");
    expect(loadThen).toBeGreaterThan(-1);
    expect(staleLoadGuard).toBeGreaterThan(loadThen);
    expect(publishProjects).toBeGreaterThan(staleLoadGuard);
    expect(pageSource).toContain("projectLoad.current?.abort()");
    expect(pageSource).toContain("projectLoad.current === controller");
    expect(pageSource).toContain("<ScopeBar");
    expect(pageSource).toContain("<RetrievalTrace");
    expect(pageSource).toContain("<SourcePanel");
    expect(pageSource).toContain("Evidencia limitada al alcance autorizado");
    expect(pageSource).toContain('lg:grid-cols-[minmax(0,1fr)_22rem]');
    expect(pageSource).toContain("overflow-x-hidden");
  });

  it("uses SSE by default with unique request IDs and an abortable lifecycle", () => {
    expect(pageSource).toContain("const requestId = crypto.randomUUID()");
    expect(pageSource).toContain("client.streamAgent({");
    expect(pageSource).not.toContain("answerAgent(");
    expect(pageSource).not.toContain("agent-transport");
    expect(pageSource).toContain('{ type: "meta", requestId');
    expect(pageSource).toContain('{ type: "citation", requestId');
    expect(pageSource).toContain('{ type: "complete", requestId');
    expect(pageSource).toContain('dispatch({ type: "stop" })');
    expect(pageSource).toContain('dispatch({ type: "new_conversation" })');
    expect(pageSource).toContain("{props.pendingQuestion}");
    expect(pageSource).toContain("activeRequest.current?.abort()");
    expect(pageSource).toContain("error instanceof APIError && error.status === 403");
    expect(pageSource).toContain('dispatch({ type: "select_project", projectId: "" })');
    expect(pageSource).toContain('dispatch({ type: "logout" })');
  });

  it("supports keyboard focus, live status, mobile evidence disclosure and no persistence", () => {
    expect(pageSource).toContain('aria-live="polite"');
    expect(pageSource).toContain("composerRef.current?.focus()");
    expect(pageSource).toContain("Ver trazabilidad y evidencia");
    expect(pageSource).toContain('className="sticky bottom-0');
    expect(pageSource).toContain('aria-label="Conversación del proyecto"');
    expect(pageSource).not.toContain("localStorage");
    expect(pageSource).not.toContain("sessionStorage");
    expect(pageSource).not.toContain("indexedDB");
    expect(globalStyles).toContain("@media (prefers-reduced-motion: reduce)");
    expect(globalStyles).toContain("animation-duration: 0.01ms !important");
  });
});
