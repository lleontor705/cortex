"use client";

import { useEffect } from "react";
import Link from "next/link";
import { AlertTriangle, LayoutDashboard, RotateCcw } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface RouteErrorProps {
  error: Error & { digest?: string };
  reset: () => void;
}

export default function RouteError({ error, reset }: RouteErrorProps) {
  useEffect(() => {
    console.error("Route render error", error);
  }, [error]);

  // Shipped bundles must not carry stacks: the digest is the production-safe reference.
  const stack = process.env.NODE_ENV === "production" ? null : error.stack;

  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4 py-10">
      <Card className="w-full max-w-lg border-border bg-card">
        <CardHeader className="items-center space-y-3 text-center">
          <div className="flex h-11 w-11 items-center justify-center rounded-lg border border-destructive/25 bg-destructive/10 text-destructive">
            <AlertTriangle className="h-5 w-5" aria-hidden="true" />
          </div>
          <CardTitle className="text-base">Algo salió mal</CardTitle>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Esta vista no pudo renderizarse. Reintenta la operación o vuelve al panel principal.
          </p>
        </CardHeader>

        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center justify-center gap-2">
            <Button size="sm" onClick={() => reset()} className="gap-1.5 text-xs">
              <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
              Reintentar
            </Button>
            <Link
              href="/"
              className={cn(
                buttonVariants({ variant: "outline", size: "sm" }),
                "gap-1.5 text-xs"
              )}
            >
              <LayoutDashboard className="h-3.5 w-3.5" aria-hidden="true" />
              Ir al inicio
            </Link>
          </div>

          <details className="rounded-lg border border-border bg-secondary/40 text-xs">
            <summary className="cursor-pointer select-none px-3 py-2 font-medium text-muted-foreground transition-colors hover:text-foreground">
              Detalles técnicos
            </summary>
            <div className="space-y-2 border-t border-border px-3 py-2.5">
              {error.digest ? (
                <p className="font-mono text-[11px] text-muted-foreground">
                  digest: <span className="text-foreground">{error.digest}</span>
                </p>
              ) : null}
              <p className="break-words font-mono text-[11px] text-muted-foreground">
                {error.message || "Error sin mensaje"}
              </p>
              {stack ? (
                <pre className="max-h-48 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-background/60 p-2 font-mono text-[10px] text-muted-foreground">
                  {stack}
                </pre>
              ) : null}
            </div>
          </details>
        </CardContent>
      </Card>
    </div>
  );
}
