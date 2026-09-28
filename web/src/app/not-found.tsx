"use client";

import Link from "next/link";
import {
  BrainCircuit,
  Compass,
  FolderKanban,
  Home,
  Search,
  Settings,
  Share2,
} from "lucide-react";
import { Card, CardContent } from "@/components/ui/card";

const destinations = [
  { href: "/", label: "Inicio", description: "Panel general y estado del sistema", icon: Home },
  { href: "/search", label: "Explorar", description: "Búsqueda híbrida en memoria", icon: Search },
  { href: "/memory", label: "Memoria", description: "Observaciones y sesiones", icon: BrainCircuit },
  { href: "/graph", label: "Grafo", description: "Explora el grafo de conocimiento", icon: Share2 },
  { href: "/projects", label: "Proyectos", description: "Directivas y skills MCP", icon: FolderKanban },
  { href: "/settings", label: "Servidor", description: "Configuración y conexión", icon: Settings },
] as const;

export default function NotFound() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center px-4 py-10">
      <Card className="w-full max-w-2xl border-border bg-card">
        <CardContent className="space-y-6 p-6 sm:p-8">
          <div className="space-y-2 text-center">
            <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary">
              <Compass className="h-6 w-6" aria-hidden="true" />
            </div>
            <p className="font-mono text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              404
            </p>
            <h1 className="text-xl font-bold tracking-tight text-foreground">
              Página no encontrada
            </h1>
            <p className="mx-auto max-w-md text-xs leading-relaxed text-muted-foreground">
              La ruta solicitada no existe en este panel. Elige uno de los destinos disponibles para
              continuar.
            </p>
          </div>

          <nav aria-label="Rutas disponibles" className="grid grid-cols-1 gap-2.5 sm:grid-cols-2">
            {destinations.map(({ href, label, description, icon: Icon }) => (
              <Link
                key={href}
                href={href}
                className="flex items-start gap-3 rounded-lg border border-border bg-secondary/40 p-3.5 transition-colors hover:border-primary/40 hover:bg-secondary/70 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border bg-card text-muted-foreground">
                  <Icon className="h-4 w-4" aria-hidden="true" />
                </span>
                <span className="min-w-0">
                  <span className="block text-xs font-semibold text-foreground">{label}</span>
                  <span className="mt-0.5 block text-[11px] leading-relaxed text-muted-foreground">
                    {description}
                  </span>
                </span>
              </Link>
            ))}
          </nav>
        </CardContent>
      </Card>
    </div>
  );
}
