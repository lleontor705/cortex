# Guía de inteligencia de grafo y arquitectura de código

Cortex proporciona un motor de inteligencia de grafo de código y conocimiento
estático zero-CGO, portado nativamente e integrado en la plataforma de memoria
Cortex.

---

## 1. Extractor estático AST zero-CGO (`internal/domain/ast`)

El extractor AST analiza repositorios de código y extrae símbolos y relaciones
estructuradas sin enviar código a LLMs ni consumir tokens de API externos.

### Lenguajes soportados
- **Go (`.go`):** parsing nativo vía `go/parser` y `go/ast`. Extrae módulos,
  paquetes, structs, interfaces, métodos, funciones y llamadas directas.
- **Ecosistema .NET:**
  - **C# (`.cs`):** namespaces, directivas `using`, clases, interfaces, structs,
    records, enums y métodos.
  - **F# (`.fs`, `.fsi`, `.fsx`):** imports `open`, módulos, tipos
    (records/unions/clases) y funciones `let`.
  - **VB.NET (`.vb`):** `Imports`, `Namespace`, `Class`, `Interface`,
    `Structure`, `Module`, `Sub` y `Function`.
- **Java y Kotlin (`.java`, `.kt`, `.kts`):** paquetes, imports, clases, data
  classes, interfaces, objects, enums, records y funciones/métodos.
- **Rust (`.rs`):** módulos (`mod`), imports (`use`), structs, enums, traits y
  funciones (`fn`).
- **C / C++ (`.c`, `.cpp`, `.cc`, `.cxx`, `.h`, `.hpp`, `.hxx`):** directivas
  `#include`, namespaces, clases, structs y funciones.
- **PHP (`.php`):** namespaces, sentencias `use`, clases, interfaces, traits,
  enums y funciones.
- **Ruby (`.rb`):** `require`/`require_relative`, módulos, clases y métodos `def`.
- **Swift (`.swift`):** módulos, imports, clases, structs, protocols, enums,
  extensiones y funciones.
- **TypeScript / JavaScript (`.ts`, `.tsx`, `.js`, `.jsx`, `.mjs`, `.cjs`):**
  módulos ES, declaraciones de import, clases exportadas, funciones y arrow
  functions.
- **Python (`.py`, `.pyw`):** módulos, imports de paquete (`from X import Y`),
  clases, métodos y funciones.
- **SQL (`.sql`):** definiciones de tablas de base de datos (`CREATE TABLE`).

### Relaciones estructurales
- `defines`: el fichero/módulo declara un struct, clase o función.
- `imports`: el fichero importa un paquete o módulo externo.
- `calls`: una función/método invoca a otro símbolo.
- `implements`: un método implementa una interface o pertenece a un struct receptor.
- `uses`: un símbolo referencia un tipo o modelo.

---

## 2. Analítica de grafo y algoritmos (`internal/domain/graph`)

### Detección de comunidades (Louvain / Hub Partitioning)
- Particiona el grafo del proyecto en subsistemas fuertemente acoplados.
- Asigna automáticamente nombres de comunidad usando el **Hub Node** (el símbolo
  con mayor grado en el cluster).
- Calcula scores de cohesión interna para cada comunidad.

### God nodes (cuellos de botella arquitectónicos)
- Identifica hubs críticos con conectividad desproporcionada
  (`in_degree` + `out_degree`).
- Filtra automáticamente el ruido de tipos utilitarios (`string`, `error`,
  `context.Context`, etc.).

### Conexiones sorprendentes
- Puntúa y marca edges anómalos entre módulos, acoplamientos periferio-to-hub y
  edges semánticos críticos (`contradicts`, `supersedes`).

### Ciclos de dependencia (Tarjan SCC)
- Detecta dependencias circulares y bucles de import entre módulos.

### Cálculo de blast radius
- Calcula el impacto upstream y downstream al modificar un símbolo o fichero.
- Emite la lista de ficheros impactados, los callers dependientes directos y el
  porcentaje del grafo del proyecto afectado.

---

## 3. Inteligencia de código en la CLI (`cortex code`)

El comando `cortex code` expone el mismo motor AST y de grafo desde la terminal
(consulta [CLI-REFERENCE.md](CLI-REFERENCE.md#323-code) para el contrato
autoritativo del comando). `cortex code`, `cortex code help`,
`cortex code --help` y `cortex code -h` imprimen la lista de subcomandos y salen
con `0`; cada subcomandos usa `default` para `--project` por defecto.

| Subcomando | Sinopsis | Propósito |
| :--- | :--- | :--- |
| `scan` | `cortex code scan [path] [--project=NAME] [--max-files=N]` | Escanear un repositorio e indexar símbolos AST (delega en `cortex ingest`; `--max-files` por defecto `500`). |
| `symbols` | `cortex code symbols [--project=NAME] [--kind=KIND] [--file=PATH]` | Listar hasta 100 símbolos indexados, opcionalmente filtrados por kind o fichero. |
| `analyze` | `cortex code analyze [--project=NAME]` | Ejecutar analíticas Graphify: totales, cohesión media, god nodes y ciclos de import. |
| `impact` | `cortex code impact <target> [--project=NAME] [--hops=N] [--json]` | Blast radius de un símbolo o fichero (`--hops` por defecto `3`; alias `blast-radius`). |
| `diff` | `cortex code diff [--staged] [--project=NAME] [--hops=N] [--json]` | Blast radius de cambios Git sin commitear (`--staged`/`--cached`; `--hops` por defecto `3`). |
| `graph` | `cortex code graph [--project=NAME] [--format=mermaid] [--symbol=S] [--hops=N] [--max-nodes=N]` | Exportar o visualizar el grafo de dependencias (`--format` `mermaid`, `ascii`/`tree`, `json`; `--hops` por defecto `2`, `--max-nodes` por defecto `50`). |
| `map` | `cortex code map [--project=NAME] [--budget=N]` | Generar un Repo-Map compacto con presupuesto de tokens para LLMs (`--budget` por defecto `2048`; alias `repo-map`). |
| `tests` | `cortex code tests <target> [--project=NAME] [--hops=N] [--json]` | Hallar ficheros y funciones de test impactados para Fast-TDD (`--hops` por defecto `3`; alias `test-map`, `impacted-tests`). |
| `find` | `cortex code find <query> [--project=NAME] [--kind=K] [--file=PATH] [--limit=N] [--regex] [--json]` | Buscar símbolos por substring o regex (`--limit` por defecto `50`; alias `search-symbols`). |

```bash
cortex code scan . --project cortex
cortex code impact CalculateBlastRadius --project cortex --hops=2
cortex code diff --staged --json
cortex code tests CalculateBlastRadius
```

---

## 4. Endpoints de servidor y herramientas MCP

### Endpoints REST
- `GET /api/graph/project-graph?project=<name>`: carga el grafo completo de
  código y conocimiento de un proyecto.
- `GET /api/graph/analytics?project=<name>`: genera diagnósticos de salud
  arquitectónica (God nodes, comunidades, ciclos).
- `GET /api/graph/blast-radius?node_id=<id>&depth=<n>`: calcula el blast radius
  de cualquier nodo.
- `POST /api/graph/ingest-code`: extrae símbolos AST de un directorio local y los
  persiste en PostgreSQL.
- `POST /api/graph/resolve`: resuelve contradicciones de conocimiento con edges
  `supersedes`.

### Herramientas MCP
- `cortex_ingest_code`: extrae e indexa símbolos y relaciones mediante parsing
  AST estático de 2 pasadas en tablas dedicadas.
- `cortex_get_code_symbols`: consulta símbolos indexados (funciones, structs,
  interfaces, clases) con filtros y búsqueda regex.
- `cortex_code_map`: genera un mapa estructural del repositorio PageRank con
  presupuesto de tokens.
- `cortex_code_tests`: localiza los suites de test impactados usando call graphs
  inversos (esencial para Fast-TDD).
- `cortex_get_blast_radius`: consulta los símbolos y ficheros afectados al
  planificar cambios de código (soporta `include_tests: true`).
- `cortex_analyze_architecture`: analiza comunidades de subsistemas (Louvain),
  god nodes y hubs arquitectónicos.
- `cortex_detect_cycles`: detecta dependencias circulares y bucles de import
  (Tarjan SCC).
- `cortex_relate`: establece relaciones tipadas entre observaciones y entidades.
- `cortex_graph` / `cortex_graph_path`: recorre vecindarios y calcula caminos más
  cortos en el grafo de conocimiento.
- `cortex_graph_subgraph`: recorre subgrafos heterogéneos acotados (observaciones,
  entidades, actores, sesiones).

---

## 5. Funcionalidades de la Web UI (`web/src/app/graph/page.tsx`)

- **Project Switcher:** selecciona cualquier proyecto (`cortex`, `kardex`,
  `default` o todos) para cargar su grafo completo.
- **Modal de AST Code Scanner:** escanea directorios locales (`.`,
  `D:\my-project`) para mapear símbolos al instante en PostgreSQL.
- **Vista de comunidades Louvain:** clusters funcionales distintos con código de
  colores.
- **Vista interactiva de blast radius:** resalta los nodos impactados en
  rojo/ámbar y atenúa los elementos no afectados.
- **Cajón de salud arquitectónica:** métricas en tiempo real de God nodes,
  conexiones sorprendentes y ciclos.
- **Exportador de vault Obsidian:** descarga con un clic de las notas Markdown
  del proyecto con `[[WikiLinks]]`.
