import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-mono font-medium transition-colors",
  {
    variants: {
      variant: {
        default: "border border-blue-500/30 bg-blue-500/10 text-blue-600 dark:text-blue-400",
        secondary: "border border-border bg-secondary text-secondary-foreground",
        destructive: "border border-red-500/30 bg-red-500/10 text-red-600 dark:text-red-400",
        success: "border border-emerald-500/30 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
        warning: "border border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400",
        outline: "text-foreground border border-border bg-transparent",
        purple: "border border-purple-500/30 bg-purple-500/10 text-purple-600 dark:text-purple-400",
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
