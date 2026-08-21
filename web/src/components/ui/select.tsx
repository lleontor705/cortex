import * as React from "react";
import { cn } from "@/lib/utils";

export interface SelectProps
  extends React.SelectHTMLAttributes<HTMLSelectElement> {}

const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, children, ...props }, ref) => {
    return (
      <select
        className={cn(
          "flex h-9 w-full rounded-lg border border-slate-700 bg-slate-950/80 px-3 py-1 text-xs text-slate-100 shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500 focus-visible:border-blue-500 disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        ref={ref}
        {...props}
      >
        {children}
      </select>
    );
  }
);
Select.displayName = "Select";

export { Select };
