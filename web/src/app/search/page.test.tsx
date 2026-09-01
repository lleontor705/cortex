import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const pageSource = readFileSync(new URL("./page.tsx", import.meta.url), "utf8");

describe("/search page contract", () => {
  it("uses only server-granted project IDs and server-owned labels", () => {
    expect(pageSource).toContain(".agentProjects(controller.signal)");
    expect(pageSource).not.toContain("client.projects()");
    expect(pageSource).toContain("projects.find((project) => project.id === projectId)");
    expect(pageSource).toContain("client.search(cleanQuery, selectedProject.label)");
    expect(pageSource).toContain("project: selectedProject.label");
    expect(pageSource).toContain("<ScopeBar");
    expect(pageSource).not.toContain("localStorage");
  });

  it("drops stale grant loads and stale search responses", () => {
    const grantGuard = pageSource.indexOf("if (controller.signal.aborted || projectLoad.current !== controller) return;");
    expect(grantGuard).toBeGreaterThan(pageSource.indexOf(".then((eligible)"));
    expect(pageSource.indexOf("setProjects(eligible)")).toBeGreaterThan(grantGuard);
    expect(pageSource).toContain("if (controller.signal.aborted || activeSearch.current !== controller) return;");
    expect(pageSource).toContain("activeSearch.current?.abort()");
    expect(pageSource).toContain("result.reason.status === 403");
    expect(pageSource).toContain('setProjectId("")');
  });

  it("preserves partial results and distinguishes empty from unavailable indexes", () => {
    expect(pageSource).toContain("Promise.allSettled");
    expect(pageSource).toContain('setMemoryStatus("unavailable")');
    expect(pageSource).toContain('setCodeStatus("unavailable")');
    expect(pageSource).toContain("pero no encontró memorias para esta consulta");
    expect(pageSource).toContain("Los resultados de código, si existen, siguen siendo válidos");
    expect(pageSource).toContain("aún no fue generado para este proyecto");
    expect(pageSource).toContain("Fuente: memoria");
    expect(pageSource.match(/Fuente:/g)).toHaveLength(2);
  });

  it("supports keyboard focus, live feedback and a 320px-safe stacked layout", () => {
    expect(pageSource).toContain('aria-live="polite"');
    expect(pageSource).toContain("searchInput.current?.focus()");
    expect(pageSource).toContain('aria-pressed={mode === item.value}');
    expect(pageSource).toContain("overflow-x-hidden");
    expect(pageSource).toContain("min-w-0");
    expect(pageSource).toContain("sm:grid-cols-2");
    expect(pageSource).toContain("break-all");
  });
});
