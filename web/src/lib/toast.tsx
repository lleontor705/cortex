"use client";

import React, {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

export type ToastVariant = "success" | "info" | "error";

export interface ToastInput {
  message: string;
  variant?: ToastVariant;
  durationMs?: number;
}

export interface ToastRecord {
  id: string;
  message: string;
  variant: ToastVariant;
  durationMs: number;
}

export interface ToastOptions {
  durationMs?: number;
}

export interface ToastApi {
  toast: {
    success: (message: string, options?: ToastOptions) => string;
    info: (message: string, options?: ToastOptions) => string;
    error: (message: string, options?: ToastOptions) => string;
  };
  dismiss: (id: string) => void;
}

export interface ToastToneTokens {
  foreground: string;
  background: string;
  border: string;
}

/** Visible cap: rapid bursts keep the newest toasts and evict the oldest. */
export const TOAST_MAX_VISIBLE = 3;

/** Auto-hide durations in milliseconds. Errors stay longer than success/info. */
export const TOAST_DURATIONS: Record<ToastVariant, number> = {
  success: 4000,
  info: 4000,
  error: 7000,
};

const TONE_TOKENS: Record<ToastVariant, ToastToneTokens> = {
  success: {
    foreground: "var(--success)",
    background: "var(--success-bg)",
    border: "var(--success)",
  },
  info: {
    foreground: "var(--accent-primary)",
    background: "var(--bg-surface)",
    border: "var(--border-default)",
  },
  error: {
    foreground: "var(--danger)",
    background: "var(--danger-bg)",
    border: "var(--danger)",
  },
};

export function toastToneTokens(variant: ToastVariant): ToastToneTokens {
  return TONE_TOKENS[variant];
}

export function resolveDuration(variant: ToastVariant, explicitMs?: number): number {
  if (typeof explicitMs === "number" && Number.isFinite(explicitMs) && explicitMs >= 0) {
    return explicitMs;
  }
  return TOAST_DURATIONS[variant];
}

type ToastListener = (toasts: readonly ToastRecord[]) => void;
type TimerHandle = ReturnType<typeof setTimeout>;

export interface ToastTimers {
  setTimeout: (callback: () => void, ms: number) => TimerHandle;
  clearTimeout: (handle: TimerHandle) => void;
}

// Resolved lazily through globalThis so faked timers in tests are picked up.
const defaultTimers: ToastTimers = {
  setTimeout: (callback, ms) => globalThis.setTimeout(callback, ms),
  clearTimeout: (handle) => globalThis.clearTimeout(handle),
};

/**
 * Framework-agnostic engine for the toast queue. Kept free of React so the
 * ordering, cap and auto-hide semantics are deterministically testable and the
 * provider stays a thin binding over it.
 */
export class ToastStore {
  private records: readonly ToastRecord[] = [];
  private readonly timers = new Map<string, TimerHandle>();
  private readonly listeners = new Set<ToastListener>();
  private sequence = 0;

  constructor(private readonly timer: ToastTimers = defaultTimers) {}

  getSnapshot = (): readonly ToastRecord[] => this.records;

  subscribe = (listener: ToastListener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  push = (input: ToastInput): string => {
    const variant = input.variant ?? "info";
    const record: ToastRecord = {
      id: `toast-${(this.sequence += 1)}`,
      message: input.message,
      variant,
      durationMs: resolveDuration(variant, input.durationMs),
    };

    const next = [...this.records, record];
    const overflow = next.length - TOAST_MAX_VISIBLE;
    if (overflow > 0) {
      for (const evicted of next.slice(0, overflow)) {
        this.disarm(evicted.id);
      }
      next.splice(0, overflow);
    }

    this.records = next;
    this.arm(record);
    this.emit();
    return record.id;
  };

  dismiss = (id: string): void => {
    if (!this.records.some((record) => record.id === id)) return;
    this.records = this.records.filter((record) => record.id !== id);
    this.disarm(id);
    this.emit();
  };

  private arm(record: ToastRecord): void {
    if (record.durationMs <= 0) return;
    this.timers.set(
      record.id,
      this.timer.setTimeout(() => {
        this.timers.delete(record.id);
        this.dismiss(record.id);
      }, record.durationMs),
    );
  }

  private disarm(id: string): void {
    const handle = this.timers.get(id);
    if (handle === undefined) return;
    this.timer.clearTimeout(handle);
    this.timers.delete(id);
  }

  private emit(): void {
    for (const listener of this.listeners) listener(this.records);
  }
}

const ToastContext = createContext<ToastApi | null>(null);

const NOOP_TOAST_API: ToastApi = {
  toast: { success: () => "", info: () => "", error: () => "" },
  dismiss: () => {},
};

export function resolveToastApi(context: ToastApi | null, nodeEnv: string | undefined): ToastApi {
  if (context) return context;
  if (nodeEnv !== "production") {
    throw new Error("useToast must be used within a ToastProvider");
  }
  return NOOP_TOAST_API;
}

export function useToast(): ToastApi {
  return resolveToastApi(useContext(ToastContext), process.env.NODE_ENV);
}

export interface ToastProviderProps {
  children?: React.ReactNode;
  store?: ToastStore;
}

export function ToastProvider({ children, store: providedStore }: ToastProviderProps) {
  const storeRef = useRef<ToastStore | null>(null);
  if (storeRef.current === null) {
    storeRef.current = providedStore ?? new ToastStore();
  }
  const store = storeRef.current;
  const [toasts, setToasts] = useState<readonly ToastRecord[]>(() => store.getSnapshot());

  useEffect(() => store.subscribe(setToasts), [store]);

  const api = useMemo<ToastApi>(
    () => ({
      toast: {
        success: (message, options) =>
          store.push({ message, variant: "success", durationMs: options?.durationMs }),
        info: (message, options) =>
          store.push({ message, variant: "info", durationMs: options?.durationMs }),
        error: (message, options) =>
          store.push({ message, variant: "error", durationMs: options?.durationMs }),
      },
      dismiss: store.dismiss,
    }),
    [store],
  );

  return React.createElement(
    ToastContext.Provider,
    { value: api },
    children,
    React.createElement(Toaster, { toasts, onDismiss: api.dismiss }),
  );
}

export interface ToasterProps {
  toasts: readonly ToastRecord[];
  onDismiss: (id: string) => void;
}

export function Toaster({ toasts, onDismiss }: ToasterProps) {
  return React.createElement(
    "div",
    {
      "data-testid": "toaster",
      role: "region",
      "aria-label": "Notificaciones",
      "aria-live": "polite",
      style: {
        position: "fixed",
        right: "16px",
        bottom: "16px",
        zIndex: 50,
        display: "flex",
        flexDirection: "column",
        gap: "8px",
        maxWidth: "360px",
        fontFamily: "var(--font-sans)",
      },
    } as React.ComponentProps<"div">,
    toasts.map((toast) => renderToast(toast, onDismiss)),
  );
}

function renderToast(toast: ToastRecord, onDismiss: (id: string) => void): React.ReactElement {
  const tone = toastToneTokens(toast.variant);
  return React.createElement(
    "div",
    {
      key: toast.id,
      "data-testid": "toast",
      "data-variant": toast.variant,
      role: toast.variant === "error" ? "alert" : "status",
      style: {
        display: "flex",
        alignItems: "flex-start",
        gap: "12px",
        padding: "12px 14px",
        borderRadius: "var(--radius-lg)",
        border: `1px solid ${tone.border}`,
        background: tone.background,
        color: "var(--text-primary)",
        boxShadow: "var(--shadow-elevation)",
      },
    } as React.ComponentProps<"div">,
    React.createElement(
      "span",
      { style: { flex: 1, color: tone.foreground, fontSize: "13px", lineHeight: 1.4 } },
      toast.message,
    ),
    React.createElement(
      "button",
      {
        type: "button",
        onClick: () => onDismiss(toast.id),
        "aria-label": "Descartar notificación",
        style: {
          flexShrink: 0,
          border: "none",
          background: "transparent",
          color: "var(--text-muted)",
          cursor: "pointer",
          fontSize: "14px",
          lineHeight: 1,
          padding: "2px",
        },
      } as React.ComponentProps<"button">,
      "\u00d7",
    ),
  );
}
