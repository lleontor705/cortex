"use client";

import React, { useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Search,
  Sliders,
  Sparkles,
  Zap,
  Tag,
  Clock,
  CheckCircle,
  FolderGit2,
  Layers,
  ArrowRight,
  Copy,
  Check,
} from "lucide-react";

export default function SearchPlaygroundPage() {
  const { client } = useAuth();
  const [query, setQuery] = useState("");
  const [projectFilter, setProjectFilter] = useState("");
  const [results, setResults] = useState<Observation[]>([]);
  const [count, setCount] = useState<number | null>(null);
  const [isSearching, setIsSearching] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);
  const [copiedId, setCopiedId] = useState<string | null>(null);

  const handleSearch = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!client || !query.trim()) return;
    setIsSearching(true);
    setHasSearched(true);
    try {
      const resp = await client.search(query, projectFilter);
      setResults(resp.value || []);
      setCount(resp.Count || 0);
    } catch (err: any) {
      alert("Error en la búsqueda: " + (err.message || err));
    } finally {
      setIsSearching(false);
    }
  };

  const copyId = (id: string) => {
    navigator.clipboard.writeText(id);
    setCopiedId(id);
    setTimeout(() => setCopiedId(null), 1800);
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold tracking-tight text-white flex items-center gap-2.5">
          <Search className="h-6 w-6 text-blue-500" />
          Retrieval Playground & Búsqueda Híbrida
        </h1>
        <p className="text-xs text-slate-400 mt-1">
          Motor de recuperación semántica con ponderación combinada de BM25, similitud vectorial coseno y reranking de grafo
        </p>
      </div>

      {/* Search Input Card */}
      <Card className="p-5 bg-slate-900/70 border-slate-800 shadow-xl">
        <form onSubmit={handleSearch} className="space-y-3.5">
          <div className="flex flex-wrap gap-3">
            <div className="flex-1 min-w-[280px] relative">
              <Search className="absolute left-3.5 top-1/2 -translate-y-1/2 h-4 w-4 text-slate-500" />
              <Input
                type="text"
                placeholder="Escribe una consulta semántica o técnica (ej: 'decisión base de datos postgres')..."
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                required
                className="pl-10 h-10 text-xs bg-slate-950/80"
              />
            </div>

            <div className="w-52">
              <Input
                type="text"
                placeholder="Proyecto (opcional)"
                value={projectFilter}
                onChange={(e) => setProjectFilter(e.target.value)}
                className="h-10 text-xs bg-slate-950/80"
              />
            </div>

            <Button type="submit" disabled={isSearching} className="h-10 px-5 gap-2 font-semibold shadow-lg shadow-blue-600/20">
              <Zap className="h-4 w-4" />
              <span>{isSearching ? "Buscando..." : "Ejecutar Búsqueda"}</span>
            </Button>
          </div>
        </form>
      </Card>

      {/* Results Header */}
      {hasSearched && (
        <div className="flex flex-wrap items-center justify-between gap-3 px-1">
          <div className="text-xs text-slate-400">
            Se encontraron <b className="text-white">{count ?? results.length}</b> resultados para &ldquo;<span className="text-blue-400">{query}</span>&rdquo;
          </div>
          <Badge variant="secondary" className="text-[10px] font-mono bg-slate-900/80 border-slate-800 text-slate-400">
            Ponderación: BM25 + Vector + Graph Reranking
          </Badge>
        </div>
      )}

      {/* Results List */}
      {isSearching ? (
        <Card className="p-12 text-center text-xs text-slate-500 bg-slate-900/50 border-slate-800">
          Calculando similitudes y ranking híbrido...
        </Card>
      ) : hasSearched && results.length === 0 ? (
        <Card className="p-12 text-center bg-slate-900/50 border-slate-800">
          <p className="text-xs text-slate-400">
            No se encontraron observaciones que coincidan con los criterios de búsqueda.
          </p>
        </Card>
      ) : (
        <div className="space-y-3.5">
          {results.map((item, idx) => (
            <Card key={item.id} className="p-5 bg-slate-900/80 border-slate-800 hover:border-slate-700 transition-all shadow-lg">
              <div className="flex items-start justify-between gap-3 mb-2.5">
                <div className="flex items-center gap-2.5">
                  <span className="w-6 h-6 rounded-full bg-blue-500/15 text-blue-400 border border-blue-500/30 flex items-center justify-center font-bold text-[11px]">
                    #{idx + 1}
                  </span>
                  <h3 className="text-sm font-semibold text-white">
                    {item.title}
                  </h3>
                </div>

                <Badge variant={item.type === "decision" ? "default" : item.type === "bugfix" ? "destructive" : "secondary"}>
                  {item.type}
                </Badge>
              </div>

              <p className="text-xs text-slate-300 leading-relaxed mb-3.5 whitespace-pre-wrap">
                {item.content}
              </p>

              <div className="flex flex-wrap items-center justify-between gap-2 pt-3 border-t border-slate-800/80 text-[11px] text-slate-500">
                <div className="flex items-center gap-3">
                  <span className="flex items-center gap-1.5">
                    <FolderGit2 className="h-3 w-3 text-slate-400" />
                    <b className="text-slate-300">{item.project}</b>
                  </span>
                  <span>•</span>
                  <span>Confianza: <b className="text-emerald-400">{(item.confidence * 100).toFixed(0)}%</b></span>
                </div>

                <div className="flex items-center gap-3">
                  <button
                    onClick={() => copyId(item.id)}
                    className="flex items-center gap-1 text-[10px] font-mono text-slate-400 hover:text-slate-200 transition-colors"
                    title="Copiar ID"
                  >
                    <span>ID: {item.id.slice(0, 8)}...</span>
                    {copiedId === item.id ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
                  </button>
                  <span>•</span>
                  <div className="flex items-center gap-1">
                    <Clock className="h-3 w-3" />
                    {new Date(item.created_at).toLocaleString()}
                  </div>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
