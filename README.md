# 🧠 Cortex: Autonomous AI Memory & Project Knowledge Graph

<p align="center">
  <img src="docs/assets/architecture.svg" alt="Cortex Architecture" width="100%" />
</p>

<p align="center">
  <a href="#features"><img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&logo=go" alt="Go Version"/></a>
  <a href="#features"><img src="https://img.shields.io/badge/Next.js-15-000000?style=flat&logo=next.js" alt="Next.js"/></a>
  <a href="#features"><img src="https://img.shields.io/badge/PostgreSQL-16%20Self--Hosted-336791?style=flat&logo=postgresql" alt="Self-Hosted Postgres"/></a>
  <a href="#features"><img src="https://img.shields.io/badge/MCP-Streamable%20HTTP-8B5CF6?style=flat" alt="MCP Protocol"/></a>
  <a href="#features"><img src="https://img.shields.io/badge/Zero--CGO-Pure%20Go-10B981?style=flat" alt="Zero CGO"/></a>
</p>

**Cortex** es una plataforma autoalojada de **memoria episódica autónoma, gobernanza de proyectos y grafo de conocimiento de código** diseñada para agentes de IA (Cursor, Claude Code, Cline, Windsurf) y equipos de desarrollo.

Combina extracción estática de código AST Zero-CGO (.NET C#/F#/VB, Java, Kotlin, Rust, C/C++, PHP, Ruby, Swift, Go, TS/JS, Python, SQL), clustering de comunidades (Louvain/Leiden), análisis de blast radius, gobernanza de proyectos, búsqueda híbrida (BM25 + Vectores) y persistencia segura respaldada por PostgreSQL autoalojado de instancia única y SQLite local.

---

## ⚡ Capacidades Principales

### 1. 🌐 Grafo de Código & Conocimiento por Proyecto (Estilo Graphify)
- **Extractor AST Nativo Políglota (Zero-CGO):** Analiza código en Go puro para **.NET** (`.cs`, `.fs`, `.vb`), **Java/Kotlin** (`.java`, `.kt`), **Rust** (`.rs`), **C/C++** (`.c`, `.cpp`, `.h`, `.hpp`), **PHP** (`.php`), **Ruby** (`.rb`), **Swift** (`.swift`), **TypeScript/JavaScript** (`.ts`, `.tsx`, `.js`), **Python** (`.py`), **Go** (`.go`) y **SQL** (`.sql`) sin enviar código a LLMs externos ni gastar tokens de API.
- **Clustering de Comunidades (Louvain/Hubs):** Agrupa automáticamente los subsistemas funcionales del proyecto y etiqueta los hubs arquitectónicos.
- **God Nodes & Detección de Ciclos:** Identifica cuellos de botella (`in_degree`/`out_degree`) y dependencias circulares (Tarjan SCC).
- **Cálculo de Blast Radius:** Mide el impacto porcentual y lista los archivos/funciones afectados al modificar cualquier componente.
- **Reconciliación Incremental en Refactorizaciones:** Si el archivo `A` antes dependía de `B` y ahora depende de `B` y `C`, Cortex actualiza y reconcilia las relaciones de forma instantánea.
- **Exportador a Obsidian Vault:** Descarga notas Markdown interconectadas con enlaces `[[WikiLinks]]`.

<p align="center">
  <img src="docs/assets/graph_workflow.svg" alt="Cortex Graph Workflow" width="100%" />
</p>

### 2. 🧠 Memoria Episódica & Aprendizaje Continuo
- Registra decisiones de arquitectura, correcciones de bugs, patrones y descubrimientos técnicos.
- **Búsqueda Híbrida Inteligente:** Combina BM25 de texto completo con similitud vectorial (Gemini, OpenAI, Ollama, pgvector, Qdrant) y ponderación de frescura/importancia.
- **Workers Autónomos en Background:** Reorganización periódica de grafos, detección de contradicciones y resolución de conflictos (`supersedes`).

<p align="center">
  <img src="docs/assets/memory_lifecycle.svg" alt="Cortex Memory Lifecycle" width="100%" />
</p>

### 3. 🛡️ Gobernanza de Proyectos & Autoalojamiento
- **PostgreSQL Autoalojado (RLS de Instancia Única):** El servidor single-tenant fija `tenant_id` y `workspace_id` por configuración y ejecuta cada operación bajo RLS forzado.
- **Reglas de Proyecto & Prompts del Sistema:** Inyección dinámica de reglas corporativas y skills en cada consulta de los agentes.

---

## 📦 Instalación

### Homebrew (macOS / Linux)

```bash
brew install lleontor705/tap/cortex
```

### Go Install (Multiplataforma)

```bash
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest
```

### Binarios Precompilados & Compilación desde Fuente

Descarga directa de binarios (Windows, macOS, Linux) desde [GitHub Releases](https://github.com/lleontor705/cortex/releases), o compila localmente desde el código fuente:

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build
```

---

## 🚀 Inicio Rápido

### 1. Modo Local (SQLite, CLI & TUI)

```bash
# Configurar integración automática con perfiles modulares (dev, minimal, agent)
cortex setup claude-code --profile=dev
cortex setup opencode --profile=agent

# Ver estado del modo operativo (local, híbrido, server)
cortex status

# Diagnóstico de base de datos e índices
cortex doctor

# Búsqueda adaptativa con clasificación de complejidad
cortex search "decisión de arquitectura" --mode=auto

# Index a repository once (AST symbols and relations)
cortex ingest ./internal --project cortex

# Keep the index fresh with the continuous file watcher daemon
cortex watch . --project cortex

# Snapshot the local SQLite database before risky operations
cortex backup ~/backups/cortex.db

# Check for a newer release without installing it
cortex update --check

# Lanzar interfaz interactiva en terminal (con selector de perfiles integrado)
cortex tui
```

### 2. Embedded Web UI & Server Mode

The web UI is compiled into the `cortex` binary and served on the **same host and
port as the HTTP API**. There is no separate web container or second port: the UI
ships inside the server image.

#### Local (single binary, SQLite)

```bash
cortex serve
```

On the first boot Cortex prints the web access key **once**. Open
[http://localhost:7438](http://localhost:7438), paste the key into the UI, and it
is remembered in the browser. Later rotations use:

```bash
cortex web key show        # location, presence, and prefix (never the plaintext)
cortex web key regenerate  # mint a replacement and print it once
```

See [Embedded Web UI](docs/embedded-web.md) for the at-rest key format (0600,
prefix + HMAC digest), rotation semantics, runtime `/config.js` endpoint
injection, and the per-mode (`local`/`hybrid`/`server`) capabilities.

The local `serve` mux also serves the five web-critical parity endpoints
(`/api/me`, `/api/stats`, `/api/projects`, `/api/agent/projects`,
`/api/graph/project-graph`) behind the same `http.token` gate.

#### Server mode (self-hosted, single-tenant PostgreSQL)

```bash
cortex --mode server
```

`--mode server` runs the self-hosted single-tenant composition: a PostgreSQL 16+
backing store with one static bearer (`http.token`) authenticating a synthetic
constant principal assembled from `server.tenant_id`, `server.workspace_id`, and
`server.principal_subject`. It serves the same embedded UI at `/`, the
authenticated REST API at `/api/`, and Streamable HTTP MCP at `/mcp`; the web
access key stays an independent UI credential.

The official image runs the same binary. The bundled Compose stack brings up
PostgreSQL plus the server (UI and API on `:7438`):

```bash
# 1. Configure environment variables (optional)
cp .env.example .env

# 2. Start PostgreSQL + Cortex server
docker compose up -d
```

> [!TIP]
> On first boot the server prints the web access key once; paste it into the UI. To
> read it from the container logs run:
> ```bash
> docker compose logs cortex-server
> ```

---

## 🔌 Integración con Agentes MCP (Cursor, Claude Code, Cline)

Agrega Cortex como servidor MCP en tu editor o agente:

### Configuración MCP (Streamable HTTP):

```json
{
  "mcpServers": {
    "cortex": {
      "url": "https://tu-servidor-cortex.railway.app/mcp",
      "headers": {
        "Authorization": "Bearer TU_BEARER_TOKEN"
      }
    }
  }
}
```

### Perfiles Modulares y Herramientas MCP:
Cortex organiza su catálogo en perfiles modulares (`--tools=agent|dev|minimal`):
- **Memoria Episódica & Búsqueda:** `cortex_save`, `cortex_update`, `cortex_get_observation`, `cortex_context`, `cortex_session_summary`, `cortex_search` (FTS5 + Vectores + HippoRAG + Adaptive-RAG), `cortex_get_agent_context`.
- **Grafo de Conocimiento & Linaje:** `cortex_relate`, `cortex_graph`, `cortex_graph_path`, `cortex_revision_history`, `cortex_handoff` (handoff idempotente entre agentes).
- **Inteligencia de Código AST (Zero-CGO):** `cortex_ingest_code` (extracción estática políglota), `cortex_get_code_symbols`, `cortex_code_map` (PageRank repo map), `cortex_code_tests` (Fast-TDD test impact), `cortex_get_blast_radius`, `cortex_detect_cycles`, `cortex_analyze_architecture`.
- **Gobernanza & Estado:** `cortex_get_rules`, `cortex_save_rule`, `cortex_get_status` (modo SQLite/Postgres y capacidades). En modo Server también: `cortex_get_project_context`, `cortex_list_skills`, `cortex_get_skill`, `cortex_resolve_query`.

---

## 📚 Documentación Técnica

- [Guía de Inteligencia de Grafos & AST](docs/GRAPH_INTELLIGENCE.md)
- [Catálogo de Herramientas MCP](docs/MCP.md)
- [Referencia de API HTTP REST](docs/HTTP-API.md)
- [Arquitectura del Sistema](docs/ARCHITECTURE.md)
- [Configuración Multi-Formato](docs/CONFIGURATION.md)
- [Exportación a Obsidian](docs/OBSIDIAN_EXPORT.md)
- [Despliegue en Producción (Server & Docker)](docs/SERVER.md)
- [Embedded Web UI & Key Lifecycle](docs/embedded-web.md)

---

## 🛠️ Comandos de Desarrollo

```bash
# Descargar dependencias y compilar binario
go mod download
make build

# Ejecutar suite de pruebas unitarias y de integración
go test -v -count=1 ./...

# Linter oficial
golangci-lint run ./...

# Compilar y embeber la web ANTES de make build (ver docs/embedded-web.md)
make web-build
```

---

<p align="center">
  <b>Cortex 2.0</b> • Diseñado para potenciar el desarrollo de software asistido por IA de forma duradera y confiable.
</p>
