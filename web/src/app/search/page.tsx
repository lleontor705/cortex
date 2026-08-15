"use client";

import React, { useState } from "react";
import { useAuth } from "@/lib/auth-context";
import { Observation } from "@/lib/api";
import {
  Search,
  Sliders,
  Sparkles,
  Zap,
  Tag,
  Clock,
  CheckCircle,
  FolderGit2,
} from "lucide-react";

export default function SearchPlaygroundPage() {
  const { client } = useAuth();
  const [query, setQuery] = useState("");
  const [projectFilter, setProjectFilter] = useState("");
  const [results, setResults] = useState<Observation[]>([]);
  const [count, setCount] = useState<number | null>(null);
  const [isSearching, setIsSearching] = useState(false);
  const [hasSearched, setHasSearched] = useState(false);

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

  return (
    <div>
      <div style={{ marginBottom: "24px" }}>
        <h1 style={{ fontSize: "24px", fontWeight: "700", marginBottom: "4px" }}>
          Retrieval Playground & Búsqueda Híbrida
        </h1>
        <p style={{ color: "var(--text-secondary)", fontSize: "14px" }}>
          Prueba de recuperación semántica con ponderación combinada de BM25, similitud vectorial coseno y expansión de grafo
        </p>
      </div>

      {/* Search Input Card */}
      <div className="card" style={{ marginBottom: "24px" }}>
        <form onSubmit={handleSearch} style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
          <div style={{ display: "flex", gap: "12px", flexWrap: "wrap" }}>
            <div style={{ flex: 1, minWidth: "280px", position: "relative" }}>
              <input
                type="text"
                className="input"
                style={{ paddingLeft: "38px", fontSize: "14px" }}
                placeholder="Escribe una consulta semántica o técnica (ej: 'decisión base de datos postgres')..."
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                required
              />
              <Search
                size={18}
                color="var(--text-muted)"
                style={{ position: "absolute", left: "12px", top: "50%", transform: "translateY(-50%)" }}
              />
            </div>

            <div style={{ width: "180px" }}>
              <input
                type="text"
                className="input"
                placeholder="Proyecto (opcional)"
                value={projectFilter}
                onChange={(e) => setProjectFilter(e.target.value)}
              />
            </div>

            <button type="submit" className="btn btn-primary" disabled={isSearching}>
              <Zap size={15} />
              <span>{isSearching ? "Buscando..." : "Ejecutar Búsqueda"}</span>
            </button>
          </div>
        </form>
      </div>

      {/* Results Header */}
      {hasSearched && (
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "16px" }}>
          <div style={{ fontSize: "14px", color: "var(--text-secondary)" }}>
            Se encontraron <b>{count ?? results.length}</b> resultados para &ldquo;<span style={{ color: "var(--text-primary)" }}>{query}</span>&rdquo;
          </div>
          <span className="badge badge-zinc">
            Ponderación: BM25 + Vector + Graph Reranking
          </span>
        </div>
      )}

      {/* Results List */}
      {isSearching ? (
        <div className="card" style={{ textAlign: "center", padding: "40px", color: "var(--text-muted)" }}>
          Calculando similitudes y ranking híbrido...
        </div>
      ) : hasSearched && results.length === 0 ? (
        <div className="card" style={{ textAlign: "center", padding: "40px" }}>
          <p style={{ color: "var(--text-secondary)" }}>
            No se encontraron observaciones que coincidan con los criterios de búsqueda.
          </p>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
          {results.map((item, idx) => (
            <div key={item.id} className="card" style={{ padding: "18px" }}>
              <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-start", marginBottom: "8px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
                  <span
                    style={{
                      width: "24px",
                      height: "24px",
                      borderRadius: "50%",
                      backgroundColor: "rgba(59, 130, 246, 0.15)",
                      color: "var(--accent-primary)",
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                      fontWeight: "700",
                      fontSize: "11px",
                    }}
                  >
                    #{idx + 1}
                  </span>
                  <h3 style={{ fontSize: "15px", fontWeight: "600", color: "var(--text-primary)" }}>
                    {item.title}
                  </h3>
                </div>

                <span className={`badge ${item.type === "decision" ? "badge-blue" : item.type === "bugfix" ? "badge-amber" : "badge-zinc"}`}>
                  {item.type}
                </span>
              </div>

              <p style={{ color: "var(--text-secondary)", fontSize: "13px", lineHeight: "1.6", marginBottom: "12px", whiteSpace: "pre-wrap" }}>
                {item.content}
              </p>

              <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", fontSize: "11px", color: "var(--text-muted)", borderTop: "1px solid var(--border-subtle)", paddingTop: "10px" }}>
                <div style={{ display: "flex", alignItems: "center", gap: "12px" }}>
                  <span style={{ display: "flex", alignItems: "center", gap: "4px" }}>
                    <FolderGit2 size={12} />
                    {item.project}
                  </span>
                  <span>•</span>
                  <span>Confianza: {(item.confidence * 100).toFixed(0)}%</span>
                </div>

                <div style={{ display: "flex", alignItems: "center", gap: "4px" }}>
                  <Clock size={11} />
                  {new Date(item.created_at).toLocaleString()}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
