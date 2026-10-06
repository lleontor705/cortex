# Cortex

**Cortex** es una plataforma auto-hospedada de **memoria episódica autónoma,
gobierno de proyectos y un grafo de conocimiento de código**, construida para
agentes de codificación AI (Cursor, Claude Code, Cline, Windsurf, OpenCode) y
equipos de desarrollo.

Combina extracción estática AST zero-CGO, clustering de comunidades, análisis de
blast radius, gobierno de proyectos, retrieval híbrido (BM25 + vectores) y
persistencia segura respaldada por SQLite local o un servidor PostgreSQL
auto-hospedado single-tenant.

## Capacidades

### Grafo de código y conocimiento

- **Extractor AST políglota (Zero-CGO)**: analiza .NET, Java/Kotlin, Rust, C/C++,
  PHP, Ruby, Swift, Go, TypeScript/JavaScript, Python y SQL en Go puro, sin enviar
  código a LLMs externos.
- **Detección de comunidades y god nodes**: agrupa subsistemas funcionales, etiqueta
  hubs arquitectónicos y reporta ciclos de dependencia (Tarjan SCC).
- **Blast radius**: mide el impacto de cambiar cualquier componente y lista los
  ficheros y funciones afectados.
- **Exportación a Obsidian**: escribe un vault Markdown interconectado con
  `[[WikiLinks]]`.

### Memoria episódica y aprendizaje continuo

- Registra decisiones de arquitectura, correcciones de bugs, patrones y
  descubrimientos técnicos.
- **Retrieval híbrido**: combina BM25 de texto completo con similitud vectorial
  (OpenAI, Ollama, pgvector, Qdrant) y ponderación de frescura/importancia.
- **Workers autónomos en segundo plano**: reorganización periódica del grafo,
  detección de contradicciones y resolución de conflictos (`supersedes`).

### Gobierno y self-hosting

- **PostgreSQL single-tenant con RLS forzado**: los identificadores de tenant y
  workspace son constantes de configuración, nunca entrada del cliente.
- **Reglas de proyecto y system prompts**: inyección dinámica de reglas
  corporativas y skills del agente en cada consulta.

## Instalación

```bash
# Homebrew (macOS / Linux)
brew install lleontor705/tap/cortex

# Go toolchain
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest

# From source
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build
```

Binarios precompilados para Windows, macOS y Linux están disponibles en la
[página de GitHub Releases](https://github.com/lleontor705/cortex/releases).

## Inicio rápido

```bash
# Wire agent integrations with modular profiles (dev, minimal, agent)
cortex setup claude-code --profile=dev
cortex setup opencode --profile=agent

# Inspect the operating mode and run diagnostics
cortex status
cortex doctor

# Adaptive search with complexity classification
cortex search "architecture decision" --mode=auto

# Index a repository once, then keep it fresh with the watcher
cortex ingest ./internal --project cortex
cortex watch . --project cortex

# Interactive terminal UI
cortex tui
```

Modo servidor (web UI embebida servida desde el mismo binario y puerto que la API
HTTP):

```bash
cortex serve
```

## Documentación

- **[Arquitectura](ARCHITECTURE.md)**, **[Configuración](CONFIGURATION.md)**,
  **[Instalación](INSTALLATION.md)**, **[Distribución](distribution.md)** y
  **[Inteligencia de grafo](GRAPH_INTELLIGENCE.md)** explican los límites de
  runtime, el almacenamiento, las migraciones y el análisis del grafo de código.
- **Interfaces**: **[MCP](MCP.md)** perfiles y herramientas, endpoints de la
  **[API HTTP](HTTP-API.md)**, **[Setup de agente](AGENT-SETUP.md)**,
  **[Plugins](PLUGINS.md)** y la **[Web UI embebida](embedded-web.md)**.
- **CLI y TUI**: el **[índice de CLI](CLI.md)**, la **[referencia completa de
  CLI](CLI-REFERENCE.md)** y la **[guía TUI](TUI-GUIDE.md)**.
- **Despliegue y operaciones**: **[Despliegue de servidor](SERVER.md)**,
  **[Identidad de servidor self-hosted](server-saas.md)**,
  **[Despliegue en Railway](RAILWAY_DEPLOYMENT.md)** y el
  **[runbook de despliegue del contexto de proyecto](project-context-protocol-identity-privilege.md)**.
- **Datos y evaluación**: **[Exportación a Obsidian](OBSIDIAN_EXPORT.md)**,
  **[Benchmarks](BENCHMARKS.md)**, **[Cobertura](COVERAGE.md)**,
  **[Matriz de capacidades](capability-matrix.md)** y la
  **[Matriz de verificación](verification-matrix.md)**.
- **Especificaciones**: [Alineación de plugins](specs/plugin-alignment.md) y
  [Remediación de auditoría E2E](specs/e2e-audit-remediation.md).
- **[Índice de documentación](README.md)**: las mismas páginas agrupadas como
  lista de lectura.
