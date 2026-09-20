import * as React from "react";
import { cn } from "@/lib/utils";

interface PageHeaderProps {
  title: string;
  badge?: React.ReactNode;
  description?: string;
  actions?: React.ReactNode;
  className?: string;
}

export function PageHeader({
  title,
  badge,
  description,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-between gap-4 pb-2 border-b border-border/40",
        className
      )}
    >
      <div className="space-y-1">
        <div className="flex items-center gap-2.5 flex-wrap">
          <h1 className="text-xl sm:text-2xl font-bold tracking-tight text-foreground">
            {title}
          </h1>
          {badge}
        </div>
        {description ? (
          <p className="text-xs text-muted-foreground leading-normal max-w-2xl">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex items-center gap-2 flex-wrap">{actions}</div> : null}
    </div>
  );
}
