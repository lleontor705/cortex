import { afterEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import {
  TOAST_DURATIONS,
  TOAST_MAX_VISIBLE,
  ToastProvider,
  ToastStore,
  Toaster,
  resolveToastApi,
  toastToneTokens,
  useToast,
  type ToastApi,
} from "./toast";

afterEach(() => {
  vi.useRealTimers();
});

describe("ToastStore deterministic queue", () => {
  it("stacks toasts newest-last in insertion order", () => {
    const store = new ToastStore();
    store.push({ message: "first", variant: "success" });
    store.push({ message: "second", variant: "info" });

    expect(store.getSnapshot().map((toast) => toast.message)).toEqual(["first", "second"]);
  });

  it("caps the queue and drops the oldest toasts first", () => {
    const store = new ToastStore();
    const total = TOAST_MAX_VISIBLE + 3;
    for (let i = 1; i <= total; i += 1) {
      store.push({ message: `m${i}`, variant: "info", durationMs: 0 });
    }

    expect(store.getSnapshot()).toHaveLength(TOAST_MAX_VISIBLE);
    expect(store.getSnapshot().map((toast) => toast.message)).toEqual(
      Array.from({ length: TOAST_MAX_VISIBLE }, (_, i) => `m${total - TOAST_MAX_VISIBLE + i + 1}`),
    );
  });

  it("assigns stable, unique ids", () => {
    const store = new ToastStore();
    const first = store.push({ message: "a", variant: "info" });
    const second = store.push({ message: "b", variant: "info" });

    expect(first).not.toBe(second);
    expect(store.getSnapshot().map((toast) => toast.id)).toEqual([first, second]);
  });

  it("auto-hides a success toast after its documented duration", () => {
    vi.useFakeTimers();
    const store = new ToastStore();
    store.push({ message: "saved", variant: "success" });

    vi.advanceTimersByTime(TOAST_DURATIONS.success - 1);
    expect(store.getSnapshot()).toHaveLength(1);
    vi.advanceTimersByTime(1);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("keeps error toasts visible longer than success toasts", () => {
    vi.useFakeTimers();
    expect(TOAST_DURATIONS.error).toBeGreaterThan(TOAST_DURATIONS.success);

    const store = new ToastStore();
    store.push({ message: "boom", variant: "error" });

    vi.advanceTimersByTime(TOAST_DURATIONS.success);
    expect(store.getSnapshot()).toHaveLength(1);
    vi.advanceTimersByTime(TOAST_DURATIONS.error - TOAST_DURATIONS.success);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("honours an explicit duration override", () => {
    vi.useFakeTimers();
    const store = new ToastStore();
    store.push({ message: "quick", variant: "info", durationMs: 250 });

    vi.advanceTimersByTime(250);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("dismisses by id and ignores unknown ids", () => {
    const store = new ToastStore();
    const id = store.push({ message: "dismiss me", variant: "info" });

    store.dismiss("missing-id");
    expect(store.getSnapshot()).toHaveLength(1);
    store.dismiss(id);
    expect(store.getSnapshot()).toHaveLength(0);
    store.dismiss(id);
    expect(store.getSnapshot()).toHaveLength(0);
  });

  it("notifies subscribers on every mutation and stops after unsubscribe", () => {
    const store = new ToastStore();
    const seen: number[] = [];
    const unsubscribe = store.subscribe((toasts) => seen.push(toasts.length));

    const id = store.push({ message: "one", variant: "info" });
    store.dismiss(id);
    unsubscribe();
    store.push({ message: "two", variant: "info" });

    expect(seen).toEqual([1, 0]);
  });
});

describe("toast variant theme tokens", () => {
  it("maps each variant to a distinct existing design token", () => {
    const success = toastToneTokens("success");
    const info = toastToneTokens("info");
    const error = toastToneTokens("error");

    expect(success.foreground).toBe("var(--success)");
    expect(error.foreground).toBe("var(--danger)");
    expect(info.foreground).toBe("var(--accent-primary)");
    expect(new Set([success.foreground, info.foreground, error.foreground]).size).toBe(3);
  });
});

describe("useToast provider invariant", () => {
  it("throws a clear invariant error outside production", () => {
    expect(() => resolveToastApi(null, "development")).toThrow(/ToastProvider/);
    expect(() => resolveToastApi(null, "test")).toThrow(/ToastProvider/);
  });

  it("no-ops safely in production builds", () => {
    const api = resolveToastApi(null, "production");

    expect(api.toast.success("ignored")).toBe("");
    expect(() => api.dismiss("ignored")).not.toThrow();
  });

  it("returns the live api when a provider value exists", () => {
    const live = {
      toast: { success: () => "id", info: () => "id", error: () => "id" },
      dismiss: () => {},
    } satisfies ToastApi;

    expect(resolveToastApi(live, "production")).toBe(live);
  });
});

describe("ToastProvider and Toaster host", () => {
  it("mounts the toaster host and renders children", () => {
    const markup = renderToStaticMarkup(
      React.createElement(
        ToastProvider,
        null,
        React.createElement("span", { "data-probe": "child" }, "child"),
      ),
    );

    expect(markup).toContain('data-probe="child"');
    expect(markup).toContain('data-testid="toaster"');
  });

  it("exposes the stable { toast: { success, info, error }, dismiss } contract", () => {
    const holder: { api?: ToastApi } = {};
    function Capture(): null {
      holder.api = useToast();
      return null;
    }

    renderToStaticMarkup(React.createElement(ToastProvider, null, React.createElement(Capture)));

    expect(holder.api).toBeDefined();
    expect(typeof holder.api?.toast.success).toBe("function");
    expect(typeof holder.api?.toast.info).toBe("function");
    expect(typeof holder.api?.toast.error).toBe("function");
    expect(typeof holder.api?.dismiss).toBe("function");
  });

  it("throws when useToast renders outside the provider", () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    function OutsideProbe(): null {
      useToast();
      return null;
    }

    expect(() => renderToStaticMarkup(React.createElement(OutsideProbe))).toThrow(/ToastProvider/);
    consoleError.mockRestore();
  });

  it("renders every variant with theme tokens and a dismiss control", () => {
    const store = new ToastStore();
    store.push({ message: "Saved", variant: "success", durationMs: 0 });
    store.push({ message: "Working", variant: "info", durationMs: 0 });
    store.push({ message: "Failed", variant: "error", durationMs: 0 });

    const markup = renderToStaticMarkup(
      React.createElement(Toaster, { toasts: store.getSnapshot(), onDismiss: () => {} }),
    );

    expect(markup).toContain("var(--success)");
    expect(markup).toContain("var(--accent-primary)");
    expect(markup).toContain("var(--danger)");
    expect(markup.match(/data-testid="toast"/g)).toHaveLength(3);
    expect(markup.match(/aria-label="Descartar notificación"/g)).toHaveLength(3);
  });

  it("renders newest-last so the most recent toast is last in the DOM", () => {
    const store = new ToastStore();
    store.push({ message: "older", variant: "info", durationMs: 0 });
    store.push({ message: "newer", variant: "info", durationMs: 0 });

    const markup = renderToStaticMarkup(
      React.createElement(Toaster, { toasts: store.getSnapshot(), onDismiss: () => {} }),
    );

    expect(markup.indexOf("older")).toBeGreaterThanOrEqual(0);
    expect(markup.indexOf("older")).toBeLessThan(markup.indexOf("newer"));
  });
});
