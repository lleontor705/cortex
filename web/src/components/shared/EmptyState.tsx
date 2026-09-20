import * as React from "react";
import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";

interface EmptyStateProps {
  icon?: LucideIcon;
  title: string;
  description?: string;
  action?: React.ReactNode;
  className?: string;
}

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: EmptyStateProps) {
  return (
    <div
      className={cn(
        "py-10 px-6 text-center border border-dashed border-border rounded-lg bg-card/40 flex flex-col items-center justify-center",
        className
      )}
    >
      {Icon ? (
        <div className="w-10 h-10 rounded-lg bg-secondary/80 border border-border flex items-center justify-center mb-3 text-muted-foreground">
          <Icon className="h-5 w-5" aria-hidden="true" />
        </div>
      ) : null}
      <h3 className="text-xs font-semibold text-foreground tracking-tight">{title}</h3>
      {description ? (
        <p className="text-[11px] text-muted-foreground mt-1 max-w-sm leading-relaxed">
          {description}
        </p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
