import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-[11px] font-mono font-medium transition-colors",
  {
    variants: {
      variant: {
        default: "border border-blue-500/25 bg-blue-500/10 text-blue-600 dark:text-blue-400",
        secondary: "border border-border bg-secondary text-secondary-foreground",
        destructive: "border border-red-500/25 bg-red-500/10 text-red-600 dark:text-red-400",
        success: "border border-emerald-500/25 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
        warning: "border border-amber-500/25 bg-amber-500/10 text-amber-600 dark:text-amber-400",
        outline: "text-foreground border border-border bg-transparent",
        purple: "border border-slate-600/30 bg-slate-800/60 text-slate-300",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLDivElement>,
    VariantProps<typeof badgeVariants> {}

function Badge({ className, variant, ...props }: BadgeProps) {
  return <div className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export { Badge, badgeVariants };
