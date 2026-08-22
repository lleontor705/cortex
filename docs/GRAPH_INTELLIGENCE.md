# Graph Intelligence & Code Architecture Guide

Cortex provides a zero-CGO static code and knowledge graph intelligence engine, natively ported and integrated into the Cortex memory platform.

---

## 1. Zero-CGO Static AST Extractor (`internal/domain/ast`)

The AST extractor analyzes codebase repositories and extracts structured symbols and relationships without sending code to LLMs or consuming external API tokens.

### Supported Languages
- **Go (`.go`):** Native parsing via `go/parser` and `go/ast`. Extracts modules, packages, structs, interfaces, methods, functions, and direct call invocations.
- **.NET Ecosystem:**
  - **C# (`.cs`):** Namespaces, `using` directives, classes, interfaces, structs, records, enums, and methods.
  - **F# (`.fs`, `.fsi`, `.fsx`):** `open` imports, modules, types (records/unions/classes), and `let` functions.
  - **VB.NET (`.vb`):** `Imports`, `Namespace`, `Class`, `Interface`, `Structure`, `Module`, `Sub`, and `Function`.
- **Java & Kotlin (`.java`, `.kt`, `.kts`):** Packages, imports, classes, data classes, interfaces, objects, enums, records, and functions/methods.
- **Rust (`.rs`):** Modules (`mod`), imports (`use`), structs, enums, traits, and functions (`fn`).
- **C / C++ (`.c`, `.cpp`, `.cc`, `.cxx`, `.h`, `.hpp`, `.hxx`):** `#include` directives, namespaces, classes, structs, and functions.
- **PHP (`.php`):** Namespaces, `use` statements, classes, interfaces, traits, enums, and functions.
- **Ruby (`.rb`):** `require`/`require_relative`, modules, classes, and `def` methods.
- **Swift (`.swift`):** Modules, imports, classes, structs, protocols, enums, extensions, and functions.
- **TypeScript / JavaScript (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs`):** ES modules, import declarations, exported classes, functions, and arrow functions.
- **Python (`.py`, `.pyw`):** Modules, package imports (`from X import Y`), classes, methods, and functions.
- **SQL (`.sql`):** Database table definitions (`CREATE TABLE`).

### Structural Relationships
- `defines`: File/module declares a struct, class, or function.
- `imports`: File imports an external package or module.
- `calls`: Function/method invokes another symbol.
- `implements`: Method implements an interface or belongs to a receiver struct.
- `uses`: Symbol references a type or model.

---

## 2. Graph Analytics & Algorithms (`internal/domain/graph`)

### Community Detection (Louvain / Hub Partitioning)
- Partitions the project graph into tightly-coupled subsystems.
- Automatically assigns community names using the **Hub Node** (the symbol with highest degree in the cluster).
- Computes internal cohesion scores for each community.

### God Nodes (Architectural Bottlenecks)
- Identifies critical hubs with disproportionate connectivity (`in_degree` + `out_degree`).
- Automatically filters utility noise types (`string`, `error`, `context.Context`, etc.).

### Surprising Connections
- Scores and flags anomalous cross-module edges, peripheral-to-hub couplings, and critical semantic edges (`contradicts`, `supersedes`).

### Dependency Cycles (Tarjan SCC)
- Detects circular dependencies and import loops across modules.

### Blast Radius Calculation
- Calculates upstream and downstream impact when modifying a symbol or file.
- Outputs the list of impacted files, direct dependent callers, and percentage of the project graph affected.

---

## 3. Server Endpoints & MCP Tools

### REST Endpoints
- `GET /api/graph/project-graph?project=<name>`: Loads the complete code and knowledge graph for a project.
- `GET /api/graph/analytics?project=<name>`: Generates architectural health diagnostics (God nodes, communities, cycles).
- `GET /api/graph/blast-radius?node_id=<id>&depth=<n>`: Computes blast radius for any node.
- `POST /api/graph/ingest-code`: Extracts AST symbols from a local directory and persists them to PostgreSQL.
- `POST /api/graph/resolve`: Resolves knowledge contradictions with `supersedes` edges.

### MCP Tools
- `cortex_get_blast_radius`: Inquires affected symbols and files when planning code changes.
- `cortex_analyze_architecture`: Analyzes subsystem communities and architectural hubs.
- `cortex_detect_cycles`: Checks for circular dependencies.
- `cortex_graph_subgraph`: Traverses bounded heterogeneous subgraphs.

---

## 4. Web UI Features (`web/src/app/graph/page.tsx`)

- **Project Switcher:** Select any project (`cortex`, `kardex`, `default`, or all) to load its complete graph.
- **AST Code Scanner Modal:** Scan local directories (`.`, `D:\my-project`) to instantly map symbols into PostgreSQL.
- **Louvain Communities View:** Distinct color-coded functional clusters.
- **Interactive Blast Radius View:** Highlights impacted nodes in red/amber and dims unaffected elements.
- **Architectural Health Drawer:** Real-time metrics for God nodes, surprising connections, and cycles.
- **Obsidian Vault Exporter:** One-click download of project Markdown notes with `[[WikiLinks]]`.
