import * as React from "react";
import { cn } from "@/lib/utils";

export type StatusType =
  | "success"
  | "warning"
  | "danger"
  | "error"
  | "info"
  | "neutral"
  | "decision"
  | "bugfix"
  | "gotcha"
  | "architecture"
  | string;

interface StatusBadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  status: StatusType;
  label?: string;
  showDot?: boolean;
}

export function StatusBadge({
  status,
  label,
  showDot = false,
  className,
  ...props
}: StatusBadgeProps) {
  const normalized = status.toLowerCase();

  let style = "border-border bg-secondary text-secondary-foreground";
  let dotColor = "bg-muted-foreground";

  if (normalized === "success" || normalized === "active" || normalized === "connected" || normalized === "pass") {
    style = "border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400";
    dotColor = "bg-emerald-500";
  } else if (normalized === "warning" || normalized === "gotcha" || normalized === "caution") {
    style = "border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-400";
    dotColor = "bg-amber-500";
  } else if (normalized === "danger" || normalized === "error" || normalized === "fail" || normalized === "blocked" || normalized === "bugfix") {
    style = "border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-400";
    dotColor = "bg-red-500";
  } else if (normalized === "info" || normalized === "decision" || normalized === "architecture" || normalized === "live") {
    style = "border-blue-500/25 bg-blue-500/10 text-blue-600 dark:text-blue-400";
    dotColor = "bg-blue-500";
  } else if (normalized === "admin" || normalized === "owner") {
    style = "border-slate-600/30 bg-slate-800/60 text-slate-200";
    dotColor = "bg-slate-400";
  }

  const displayLabel = label || status;

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] font-mono font-medium border transition-colors",
        style,
        className
      )}
      {...props}
    >
      {showDot ? (
        <span className={cn("h-1.5 w-1.5 rounded-full", dotColor)} aria-hidden="true" />
      ) : null}
      <span className="truncate">{displayLabel}</span>
    </span>
  );
}
