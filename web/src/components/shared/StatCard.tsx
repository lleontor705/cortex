import * as React from "react";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";

interface StatCardProps {
  title: string;
  value: string | number;
  subtext?: string;
  icon?: LucideIcon;
  iconClassName?: string;
  className?: string;
}

export function StatCard({
  title,
  value,
  subtext,
  icon: Icon,
  iconClassName,
  className,
}: StatCardProps) {
  return (
    <Card className={cn("p-4 sm:p-5 flex flex-col justify-between", className)}>
      <div>
        <div className="flex items-center justify-between gap-2">
          <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider block">
            {title}
          </span>
          {Icon ? (
            <Icon
              className={cn("h-4 w-4 text-muted-foreground shrink-0", iconClassName)}
              aria-hidden="true"
            />
          ) : null}
        </div>
        <div className="mt-2.5 flex items-baseline gap-2">
          <span className="text-2xl sm:text-3xl font-bold font-mono tracking-tight text-foreground">
            {value}
          </span>
        </div>
      </div>
      {subtext ? (
        <p className="mt-2 text-[11px] text-muted-foreground leading-snug">
          {subtext}
        </p>
      ) : null}
    </Card>
  );
}
