import * as React from "react";
import Link from "next/link";
import {
  BrainCircuit,
  FolderKanban,
  Layers,
  Network,
  Search,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";

export type EmptyStateVariant =
  | "memory"
  | "search"
  | "graph"
  | "projects"
  | "sessions";

export interface EmptyStateActionDescriptor {
  label: string;
  href?: string;
  onClick?: () => void;
}

export interface EmptyStateVariantSpec {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: EmptyStateActionDescriptor;
}

/**
 * Domain presets for the shared empty state. Call sites opt in through the
 * `variant` prop; explicit props always win, so the pre-variant API is
 * unchanged and pages that do not declare a variant render exactly as before.
 */
export const EMPTY_STATE_VARIANTS: Record<
  EmptyStateVariant,
  EmptyStateVariantSpec
> = {
  memory: {
    icon: BrainCircuit,
    title: "No hay observaciones en este espacio",
    description:
      "Captura notas desde tu agente MCP o el extractor LLM para poblar la memoria de este proyecto.",
    action: { label: "Crear o extraer", href: "/extract" },
  },
  search: {
    icon: Search,
    title: "La búsqueda no devolvió resultados",
    description:
      "Ajusta los términos, cambia de proyecto o revisa si el índice ya fue generado para este contexto.",
    action: { label: "Explorar memoria", href: "/memory" },
  },
  graph: {
    icon: Network,
    title: "El grafo aún no tiene nodos",
    description:
      "Selecciona un proyecto con observaciones indexadas para visualizar sus conexiones semánticas.",
    action: { label: "Ver proyectos", href: "/projects" },
  },
  projects: {
    icon: FolderKanban,
    title: "Todavía no hay proyectos registrados",
    description:
      "Crea o importa un proyecto para aislar la memoria, el código y el grafo por contexto.",
    action: { label: "Extraer contenido", href: "/extract" },
  },
  sessions: {
    icon: Layers,
    title: "Sin sesiones activas",
    description:
      "Inicia una sesión desde tu agente de código vía MCP para sincronizar el contexto en vivo.",
    action: { label: "Configurar MCP", href: "/settings" },
  },
};

interface EmptyStateProps {
  icon?: LucideIcon;
  title?: string;
  description?: string;
  action?: React.ReactNode;
  variant?: EmptyStateVariant;
  className?: string;
}

function VariantAction({ descriptor }: { descriptor: EmptyStateActionDescriptor }) {
  if (descriptor.href) {
    return (
      <Link href={descriptor.href}>
        <Button variant="outline" size="sm" className="text-xs">
          {descriptor.label}
        </Button>
      </Link>
    );
  }
  if (descriptor.onClick) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        className="text-xs"
        onClick={descriptor.onClick}
      >
        {descriptor.label}
      </Button>
    );
  }
  return null;
}

export function EmptyState({
  icon,
  title,
  description,
  action,
  variant,
  className,
}: EmptyStateProps) {
  const preset = variant ? EMPTY_STATE_VARIANTS[variant] : undefined;
  const Icon = icon ?? preset?.icon;
  const resolvedTitle = title ?? preset?.title;
  const resolvedDescription = description ?? preset?.description;
  const resolvedAction =
    action ?? (preset?.action ? <VariantAction descriptor={preset.action} /> : null);

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
      {resolvedTitle ? (
        <h3 className="text-xs font-semibold text-foreground tracking-tight">
          {resolvedTitle}
        </h3>
      ) : null}
      {resolvedDescription ? (
        <p className="text-[11px] text-muted-foreground mt-1 max-w-sm leading-relaxed">
          {resolvedDescription}
        </p>
      ) : null}
      {resolvedAction ? <div className="mt-4">{resolvedAction}</div> : null}
    </div>
  );
}
