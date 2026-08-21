import * as React from "react";
import { cn } from "@/lib/utils";

export interface InputProps
  extends React.InputHTMLAttributes<HTMLInputElement> {}

const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, type, ...props }, ref) => {
    return (
      <input
        type={type}
        className={cn(
          "flex h-9 w-full rounded-lg border border-slate-700 bg-slate-950/80 px-3 py-1 text-xs text-slate-100 shadow-sm transition-colors file:border-0 file:bg-transparent file:text-xs file:font-medium placeholder:text-slate-500 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-blue-500 focus-visible:border-blue-500 disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        ref={ref}
        {...props}
      />
    );
  }
);
Input.displayName = "Input";

export { Input };
