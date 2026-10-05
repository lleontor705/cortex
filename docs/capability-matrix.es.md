# Matriz de capacidades de Cortex v2

## Resumen

Este documento proporciona una matriz de capacidades concisa y trazable a la
fuente para Cortex v2. Todas las capacidades se verifican contra los límites de
arquitectura del repositorio, las implementaciones de fuente y los contratos de
configuración.

### Taxonomía de estado

- **Core (Zero-CGO)**: incluido en la distribución pure-Go por defecto; no
  requiere CGO, bases de datos vectoriales externas ni dependencias daemon.
- **Condicional (Build Tag)**: capacidad funcional compilada con build tags
  explícitos de Go (por ejemplo, `-tags cortex_vectors`). Los binarios de release
  habilitan `cortex_vectors`.
- **Servidor (self-hosted single-tenant)**: capacidad de servidor PostgreSQL
  self-hosted que requiere PostgreSQL 16+, alineación con el ledger de migraciones
  y un bearer estático configurado.
- **Adaptador externo**: adaptador plug-in para infraestructura externa (por
  ejemplo, Qdrant o pgvector); condicionado por build tags y configuración de
  runtime del servidor.
- **Stub degradado**: implementación de fallback segura conectada por defecto
  para mantener las garantías de zero-CGO y zero-crash cuando los adaptadores
  opcionales están ausentes.
- **Plugin de cliente**: integración con agentes externos incrustada en el binario
  o distribuida como lifecycle harness.
- **Gate de CI**: job de verificación determinista definido en
  `.github/workflows/ci.yml`; valida el comportamiento distribuido pero no es en
  sí una capacidad de runtime.

### Dimensiones de especificación y evaluación

Cada capacidad de esta matriz se evalúa en cuatro dimensiones estrictas:
1. **Alcance (Scope)**: el límite de runtime, la responsabilidad del componente o
   el tier operativo donde funciona la capacidad.
2. **Prerrequisitos**: versión de la toolchain, tags de compilación,
   infraestructura subyacente o configuración requerida para la activación.
3. **Evidencia de fuente**: enlaces relativos al repositorio hacia el código
   fuente verificado, migraciones o tests que implementan la capacidad.
4. **Limitaciones y restricciones**: límites de comportamiento concretos, vías de
   degradación o no-capacidades.

> [!IMPORTANT]
> **Aviso de reporte veraz**:
> Por las restricciones del dispatch, no se ejecutó ningún suite de tests durante
> esta sesión de mantenimiento de documentación. Las evaluaciones de gates y
> resultados de test no ejecutados en esta sesión se reportan como `ND/pending`
> (no determinado) o remiten al CI autoritativo. No se hacen afirmaciones `PASS`
> de runtime no ejecutado.

---

## 1. Modos de runtime y límites arquitectónicos

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Modo local (default)** | Core (Zero-CGO) | CLI local, TUI, HTTP local (`cortex serve`) y servidor MCP stdio | Go 1.26.5; filesystem local | [`cmd/cortex/main.go`](../cmd/cortex/main.go), [`internal/cli`](../internal/cli), [`internal/app/app.go`](../internal/app/app.go) | Ejecución SQLite embebida single-tenant conectada vía `internal/store/bundle`. El código local debe seguir siendo zero-CGO. |
| **Modo servidor** | Servidor (self-hosted single-tenant) | Servicio self-hosted single-tenant, MCP Streamable HTTP (`/mcp`), API REST, streaming SSE | Go 1.26.5; PostgreSQL 16+ con bootstrap RLS; configuración de DSN dual; bearer estático | [`cmd/cortex/main.go`](../cmd/cortex/main.go), [`internal/platform/server`](../internal/platform/server) | Requiere PostgreSQL externo; no puede ejecutar la base de datos SQLite embebida concurrentemente en el mismo proceso. Tenant y workspace son constantes de configuración, no entrada del cliente. |
| **Gate de aislamiento de arquitectura** | Core (Zero-CGO) | Verificación de aislamiento estático de dependencias e imports | Go 1.26.5 | [`internal/app/arch_test.go`](../internal/app/arch_test.go), [`AGENTS.md`](../AGENTS.md) | Hace cumplir que los paquetes de composición local nunca importen PostgreSQL, authz/identidad, Qdrant/pgvector ni paquetes del servidor. |
| **Restricción de puente de comando** | Core (Zero-CGO) | Punto de entrada de composición del binario de producción | Go 1.26.5 | [`cmd/cortex/main.go`](../cmd/cortex/main.go) | `cmd/cortex` es el único paquete puente permitido entre la composición local y `internal/platform/server`. |

---

## 2. Motor de persistencia y migración

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Línea base SQLite v2** | Core (Zero-CGO) | Esquema de persistencia embebida local | Go 1.26.5; SQLite 3 | [`migrations/v2/001_init.sql`](../migrations/v2/001_init.sql), [`internal/app/app.go`](../internal/app/app.go) | Línea base forward-only. Una sonda de arranque de solo lectura verifica SHA-256 en `cortex_meta`. Rechaza bases de datos v1, ajenas o corruptas sin mutarlas. |
| **Follow-ups de migración SQLite** | Core (Zero-CGO) | Migraciones de esquema follow-up (002, 003) | Go 1.26.5; baseline 001 aplicada | [`internal/migration/v2.go`](../internal/migration/v2.go), [`migrations/v2/003_project_artifacts.sql`](../migrations/v2/003_project_artifacts.sql) | Preflight de ledger de solo lectura (`PreflightFollowUp`) y check post-aplicación (`VerifyFollowUpApplied`). Append-only, forward-only, retención indefinida. |
| **Ledger de esquema PostgreSQL** | Servidor (self-hosted single-tenant) | Migraciones de esquema de servidor (100–109) | PostgreSQL 16+; DSN de migración privilegiado | [`migrations/v2/100_server.sql`](../migrations/v2/100_server.sql) hasta `109_scoped_code_index.sql`, [`internal/migration/postgres.go`](../internal/migration/postgres.go) | Rastreado en `cortex_server_migrations`. Los checksums históricos (100–105) son inmutables; cualquier checksum registrado inesperado aborta el rollout. |
| **Seguridad de DSN dual** | Servidor (self-hosted single-tenant) | Separación de privilegios del pool de conexión PostgreSQL | Rol de runtime no-superusuario y rol de migración privilegiado | [`internal/config/config.go`](../internal/config/config.go), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | El rol de runtime (`server.storage.dsn`) no puede ejecutar DDL; el rol de migración (`server.storage.migration_dsn`) no puede servir queries de la API. |
| **Transacciones cross-store** | Core (Zero-CGO) | Escrituras locales atómicas multi-store | Go 1.26.5; backend SQLite | [`internal/domain/interfaces.go`](../internal/domain/interfaces.go) (línea 317), [`internal/store/bundle/bundle.go`](../internal/store/bundle/bundle.go) | Escrituras multi-store orquestadas por `domain.UnitOfWork` a través de memoria, sesiones, prompts y edges de grafo con reintento de busy acotado. |

---

## 3. Stack de retrieval e inteligencia RAG

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Búsqueda léxica (FTS5 / PostgreSQL)** | Core (Zero-CGO) / Servidor | Indexado de texto completo y ranking BM25/tsvector | Go 1.26.5 (local); PostgreSQL 16+ (servidor) | [`internal/store/search/store.go`](../internal/store/search/store.go), [`internal/store/postgres/extras.go`](../internal/store/postgres/extras.go), [`internal/store/postgres/authorized_operations.go`](../internal/store/postgres/authorized_operations.go) | SQLite FTS5 pure-Go en local; búsqueda de texto completo PostgreSQL (`tsvector`/`tsquery`) en modo servidor; conjunto de candidatos acotado. |
| **Vector denso (SQLite BLOB)** | Condicional (Build Tag) | Escaneo de similitud vectorial en memoria local | Go 1.26.5; build tag `-tags cortex_vectors` | [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go), [`internal/store/sqlite/vector_store_enabled.go`](../internal/store/sqlite/vector_store_enabled.go) | Escaneo coseno en memoria de BLOBs float32 en SQLite; habilitado en binarios de release; rechaza mismatches de dimensión. |
| **Stub de vector denso** | Stub degradado | Fallback de vectores por defecto cuando se omite la build tag | Go 1.26.5 (sin `cortex_vectors`) | [`internal/vector/sqlite_blob/adapter.go`](../internal/vector/sqlite_blob/adapter.go), [`internal/store/sqlite/vector_store.go`](../internal/store/sqlite/vector_store.go) | Fallback zero-CGO que emite warning de salud degradado y devuelve `ErrVectorSearchDisabled`; cae con seguridad a la búsqueda léxica. |
| **Adaptador de vectores Qdrant** | Adaptador externo | Búsqueda vectorial distribuida para un servidor self-hosted | Go 1.26.5 con `-tags qdrant_integration`; clúster Qdrant en marcha | [`internal/vector/qdrant`](../internal/vector/qdrant), [`AGENTS.md`](../AGENTS.md) | Solo servidor; aislado por una frontera de celda de workspace inmutable; falla explícitamente si el endpoint de Qdrant no está disponible. |
| **Adaptador pgvector** | Adaptador externo | Indexado vectorial de PostgreSQL in-database | Go 1.26.5 con `-tags pgvector_integration`; PostgreSQL con extensión `vector` | [`internal/vector/pgvector`](../internal/vector/pgvector), [`AGENTS.md`](../AGENTS.md) | Solo servidor; hace cumplir la frontera de celda de workspace para prevenir filtraciones vectoriales cross-cell. |
| **Reciprocal Rank Fusion (RRF)** | Core (Zero-CGO) | Fusión de rankings híbrida sobre resultados léxicos y vectoriales | Go 1.26.5 | [`internal/retrieval/retrieval.go`](../internal/retrieval/retrieval.go) (`FuseResults`, línea 167), [`internal/store/search/store.go`](../internal/store/search/store.go) | Combina listas rankeadas usando la constante canónica $k=60$; opera sobre posiciones rankeadas verdaderas; soporta decay temporal exponencial opcional. |
| **Adaptive-RAG** | Core (Zero-CGO) | Clasificación y enrutado dinámico de complejidad de queries | Go 1.26.5 | [`internal/retrieval/adaptive.go`](../internal/retrieval/adaptive.go) | Clasificador de complejidad de 4 tiers que enruta queries por las vías de retrieval solo-léxico, híbrido, graph-expanded y multi-hop. |
| **HippoRAG (PPR)** | Core (Zero-CGO) | Retrieval asociativo vía Personalized PageRank | Go 1.26.5 | [`internal/domain/graph/ppr.go`](../internal/domain/graph/ppr.go) (`ComputePersonalizedPageRank`) | Personalized PageRank por power iteration (damping 0.85, máximo 20 iteraciones, tolerancia 1e-6) sobre grafos de memoria y de símbolos de código. |
| **LightRAG** | Core (Zero-CGO) | Resúmenes macroscópicos de comunidades arquitectónicas | Go 1.26.5 | [`internal/domain/graph/community_summaries.go`](../internal/domain/graph/community_summaries.go) (`GenerateCommunitySummaries`) | Genera resúmenes Markdown estructurados para comunidades de grafo cohesivas; acotado a subgrafos conectados. |
| **CRAG (Corrective RAG)** | Core (Zero-CGO) | Evaluación de confianza de retrieval y refinamiento de queries | Go 1.26.5 | [`internal/retrieval/crag.go`](../internal/retrieval/crag.go) | Evalúa la confianza del retrieval; dispara como mucho una reformulación local determinista acotada en baja confianza. |
| **Re-ranking ColBERT MaxSim** | Core (Zero-CGO) | Re-ranking de interacción tardía a nivel de token | Go 1.26.5 | [`internal/retrieval/late_interaction.go`](../internal/retrieval/late_interaction.go) (`ComputeWeightedMaxSimScore`) | Puntuación de similitud máxima de token ponderada por especificidad sobre los pasajes de evidencia candidatos top; cero modelos externos requeridos. |

---

## 4. Inteligencia de código y analítica de grafo

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Parser AST zero-CGO** | Core (Zero-CGO) | Análisis estático multi-lenguaje | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`internal/domain/ast`](../internal/domain/ast) | Extracción estática de símbolos y relaciones sin llamadas a LLM ni tokens de API. Soporta Go, C#, F#, VB.NET, Java, Kotlin, Rust, C/C++, PHP, Ruby, Swift, TypeScript, JavaScript, Python y SQL. |
| **Relaciones estructurales** | Core (Zero-CGO) | Extracción dirigida de relaciones de entidades de código | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go) (`CodeRelationship`, línea 39) | Extrae edges estructurales tipados: `defines`, `imports`, `calls`, `implements`, `uses`, `contains`, `extends`, `instantiates`. |
| **Watcher continuo de código** | Core (Zero-CGO) | Daemon watcher de cambios de filesystem (`cortex watch`) | Go 1.26.5; proceso activo | [`internal/domain/code/watcher.go`](../internal/domain/code/watcher.go) | Indexado AST incremental en modificaciones de ficheros; eventos de fichero con debounce; salta ficheros gitignored y assets no-source. |
| **Detección de comunidades** | Core (Zero-CGO) | Descubrimiento de clusters funcionales en el grafo del codebase | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`DetectCommunities`, línea 117) | Particiona el grafo del codebase en clusters funcionales cohesivos con etiquetado automático de Hub Node basado en centralidad de grado. |
| **Cuellos de botella arquitectónicos (God Nodes)** | Core (Zero-CGO) | Detección de nodos de alta centralidad | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`FindGodNodes`, línea 42) | Detecta hubs con conectividad desproporcionada (`in_degree` + `out_degree`) mientras filtra tipos utilitarios (`string`, `error`, `context.Context`). |
| **Ciclos de dependencia (Tarjan SCC)** | Core (Zero-CGO) | Detección de dependencias circulares y bucles de import | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`FindCycles`, línea 334) | Identifica dependencias circulares y bucles de import entre módulos vía Strongly Connected Components. |
| **Análisis de blast radius** | Core (Zero-CGO) | Propagación de impacto de cambios a través del call graph | Go 1.26.5 | [`internal/domain/graph/analytics.go`](../internal/domain/graph/analytics.go) (`CalculateBlastRadius`, línea 392) | Calcula callers upstream, dependientes downstream, ficheros afectados y porcentaje de impacto en el grafo hasta un conteo de hops acotado. |
| **Indexado de código con ámbito (migración 109)** | Servidor (self-hosted single-tenant) | Almacenamiento particionado de símbolos y relaciones AST | PostgreSQL 16+; migración 109 aplicada | [`migrations/v2/109_scoped_code_index.sql`](../migrations/v2/109_scoped_code_index.sql) | Particionado por claves compuestas `(tenant_id, workspace_id, project_id)` bajo RLS forzado; las filas legacy solo-proyecto no se han backfilleado. |

---

## 5. Interfaces de usuario y transportes de integración

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Suite de comandos CLI** | Core (Zero-CGO) | Interfaz de línea de comandos interactiva y scriptable | Go 1.26.5 | [`internal/cli`](../internal/cli), [`cmd/cortex/main.go`](../cmd/cortex/main.go) | 25+ comandos: `setup`, `search`, `save`, `context`, `stats`, `timeline`, `revisions`, `tui`, `serve`, `mcp`, `doctor`, `reindex`, `gc`, `config`, `auth`, `export`, `import`, `sync`, `merge-projects`, `ingest`, `code`, `watch`, `backup`, `update`, `web key`, `migrate`, `status`/`mode`. |
| **Terminal UI (TUI)** | Core (Zero-CGO) | Dashboard interactivo de terminal Bubble Tea (15 pantallas, 5 overlays) | Go 1.26.5; TTY interactivo | [`internal/tui`](../internal/tui), [`docs/TUI-GUIDE.md`](TUI-GUIDE.md) | Teclas globales `?` (ayuda), `ctrl+k` (paleta de comandos), `1`–`4` (conmutación de workspace), `n`, `t` (toggle de tema), `L` (modal de auth), `u`, `P`, `p`, `v`, `ctrl+c`; tablas de teclas por pantalla y peculiaridades conocidas documentadas en la guía TUI. |
| **Web UI embebida** | Core (Zero-CGO) | Sala de control web de binario único servida en el puerto HTTP | Go 1.26.5; backend SQLite; clave de acceso web de primer arranque | [`internal/web`](../internal/web), [`internal/cli/cli.go`](../internal/cli/cli.go) (`mountWebSurface`, línea 803) | Montada como el fallback `/` del mux HTTP local (default `:7438`); sin imagen de contenedor separada ni inyección `NEXT_PUBLIC_*` en build-time; las rutas explícitas de la API siguen siendo autoritativas. |
| **`cortex web key` (CLI de credencial web embebida)** | Core (Zero-CGO) | Inspeccionar y rotar la clave de acceso web embebida | Go 1.26.5 | [`internal/cli/web.go`](../internal/cli/web.go), [`internal/webkey`](../internal/webkey) | `web key show` reporta ruta y prefijo sin plaintext; `web key regenerate` rota atómicamente e imprime el nuevo secreto una vez. Nunca reemplaza `http.token` ni el bearer del servidor. |
| **`cortex code` (CLI de inteligencia de código)** | Core (Zero-CGO) | Subcomandos AST y de grafo scriptables sobre el store compartido | Go 1.26.5 | [`internal/cli/cli.go`](../internal/cli/cli.go) (`runCode`, línea 2368), [`internal/domain/code`](../internal/domain/code) | Subcomandos `scan`, `symbols`, `analyze`, `impact`, `diff`, `graph`, `map`, `tests`, `find`; lee y escribe el mismo store SQLite que la TUI y MCP. |
| **`cortex ingest` (CLI de ingesta AST)** | Core (Zero-CGO) | Escaneo único de repositorio con analítica de grafo | Go 1.26.5 | [`internal/cli/cli.go`](../internal/cli/cli.go) (`runIngest`, línea 2278), [`internal/domain/ast`](../internal/domain/ast) | Escanea `[path]` (default `.`) hasta `--max-files` (default 500); persiste símbolos e relaciones e imprime god nodes y ciclos de import detectados. `--project` usa por defecto el proyecto detectado. |
| **`cortex backup` (CLI de backup atómico SQLite)** | Core (Zero-CGO) | Snapshot atómico de base de datos en línea | Go 1.26.5; `VACUUM INTO` de SQLite | [`internal/cli/cli.go`](../internal/cli/cli.go) (`runBackup`, línea 1786) | Escribe un snapshot `.db` con timestamp o un `[path]` explícito; un destino existente se sobreescribe. Solo SQLite local. |
| **`cortex update` (CLI de self-update)** | Core (Zero-CGO) | Comprobación de release y self-update del binario | Go 1.26.5; red saliente hacia el endpoint de releases | [`internal/cli/cli.go`](../internal/cli/cli.go) (`runUpdate`, línea 2233), [`internal/update`](../internal/update) | `--check` reporta el release disponible sin instalar; un self-update fallido imprime fallbacks de instalación manual. Requiere red saliente. |
| **`cortex migrate` (CLI de migraciones)** | Core (Zero-CGO) | Control de migraciones de esquema local | Go 1.26.5; backend SQLite | [`internal/cli/cli.go`](../internal/cli/cli.go) (`runMigrate`, línea 1054), [`internal/migration`](../internal/migration) | Subcomandos `up`, `down --target N` y `status`; línea base forward-only más el ledger follow-up append-only. |
| **Servidor MCP local (stdio)** | Core (Zero-CGO) | Servidor Model Context Protocol sobre stdio | Go 1.26.5; transporte stdio | [`internal/mcp/server.go`](../internal/mcp/server.go) | El namespace es `cortex_*`. Perfiles: `agent`, `admin` y `temporal`. Opera con IDs numéricos de observación. |
| **Proxy stdio MCP remoto** | Core (Zero-CGO) | Proxy stdio que reenvía llamadas MCP al servidor remoto | Go 1.26.5; `mcp.remote.enabled: true` configurado | [`internal/mcp/proxy.go`](../internal/mcp/proxy.go) | Reenvía las llamadas MCP stdio locales al servidor Cortex remoto (`/mcp`) vía autenticación por bearer token. |
| **MCP Streamable HTTP de servidor** | Servidor (self-hosted single-tenant) | MCP Streamable HTTP autenticado en `/mcp` | PostgreSQL 16+; autenticación por bearer estático | [`internal/platform/server/http.go`](../internal/platform/server/http.go) | Expone 16+ herramientas que operan sobre UUIDs públicos vía `AuthorizedStore` bajo el binding RLS constante tenant/workspace. |
| **API HTTP local** | Core (Zero-CGO) | Servicio REST local (`cortex serve`) | Go 1.26.5; backend SQLite | [`internal/http/server.go`](../internal/http/server.go) | Rechaza bindings no-loopback sin `http.token`. Endpoint público `/health`. |
| **Endpoints de paridad (`/api/me`, `/api/stats`, `/api/projects`, `/api/agent/projects`, `/api/graph/project-graph`)** | Core (Zero-CGO) | Endpoints críticos para la web servidos sobre el puerto local respaldado por el bundle | Go 1.26.5; handlers neutrales de `internal/api` | [`internal/http/server.go`](../internal/http/server.go) (líneas 141-149) | Registrados detrás del mismo gate de token `withAuth` que envuelve todo el mux; reutiliza los handlers neutrales de `internal/api` en lugar de duplicar la lógica del servidor. |
| **API REST de servidor** | Servidor (self-hosted single-tenant) | Servicio REST single-tenant self-hosted | PostgreSQL 16+; autenticación por bearer estático | [`internal/platform/server/http.go`](../internal/platform/server/http.go) | Endpoints autenticados con bearer para memoria, analítica de grafo, blast radius, ingesta AST y estadísticas administrativas. |
| **Agente conversacional de proyecto** | Servidor (self-hosted single-tenant) | Asistente web (`POST /api/agent/answer`, `/api/agent/stream`) | Runtime de servidor; proveedor LLM configurado; principal autorizado | [`internal/domain/agent`](../internal/domain/agent), [`web/src/app/agent`](../web/src/app/agent), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | Ámbito de capacidad de solo lectura; traza de retrieval sin contenido; handles de citación validados por el servidor; memoria de navegador efímera (máx. 6 turnos). |
| **Web Dashboard (Next.js)** | Core (Node/TS) | Aplicación de sala de control Next.js 15 | Node >= 24; navegador web moderno | [`web/src`](../web/src), [`web/package.json`](../web/package.json) | Hacer cumplir la autorización BOLA; visualización de grafo; vista de clusters Louvain; blast radius interactivo; exportador de vault Obsidian. |
| **E2E de web embebida con Playwright** | Gate de CI | Flujos dirigidos por navegador contra el binario real `cortex serve` | Node >= 24; Chromium; Go 1.26.5; `make web-build` para assets frescos | [`.github/workflows/ci.yml:397`](../.github/workflows/ci.yml#L397), [`tools/e2e-web`](../tools/e2e-web) | Desbloqueo con clave de primer arranque, dashboard/memoria/búsqueda, selector de ajustes y flujos de graph-mount pasan contra la UI embebida; el informe y los traces se suben en caso de fallo. |

---

## 6. Plugins de cliente e integraciones de ecosistema

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Plugin de eventos OpenCode** | Plugin de cliente | Extensión IDE de OpenCode y event hooks | Node >= 24; runtime de OpenCode | [`plugin/opencode/cortex.ts`](../plugin/opencode/cortex.ts), [`plugin/opencode/embed.go`](../plugin/opencode/embed.go) | Incrustado vía `//go:embed`. Auto-arranca el servidor, rastrea sesiones, suprime la contaminación de sesiones por subagentes, inyecta el memory protocol en la compaction. |
| **Lifecycle hooks de Claude Code** | Plugin de cliente | Integración de lifecycle de la CLI de Claude Code | CLI de Claude Code; bash, jq, python3, timeout | [`plugin/claude-code`](../plugin/claude-code), [`docs/PLUGINS.md`](PLUGINS.md) | 5 lifecycle hooks (`SessionStart`, `UserPromptSubmit`, `SubagentStop`, `Stop`); inyección automática de contexto y seguimiento de sesiones. |
| **Proyección Markdown a Obsidian** | Core (Zero-CGO) | Exportador de vault de solo lectura con `[[WikiLinks]]` | Go 1.26.5; directorio de export | [`internal/projection/obsidian`](../internal/projection/obsidian) | Exportador de solo lectura; neutraliza nombres de dispositivo reservados de Windows (`CON`, `PRN`, `AUX`, `NUL`, `COM1-9`, `LPT1-9`); previene colisiones insensibles a mayúsculas. |
| **Sincronización de observaciones por chunks** | Core (Zero-CGO) | Sincronización de observaciones local-a-remoto | Go 1.26.5; credenciales de endpoint remoto | [`internal/sync`](../internal/sync) | Sincronización determinista por chunks entre stores SQLite locales y endpoints remotos (`cortex sync`); preflight de privacidad validado. |

---

## 7. Fronteras de seguridad, privacidad y autorización

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Aislamiento de tenant y RLS** | Servidor (self-hosted single-tenant) | segregación de datos single-tenant bajo RLS forzado | PostgreSQL 16+; `AuthorizedStore` | [`internal/authz`](../internal/authz), [`internal/store/postgres`](../internal/store/postgres) | Tenant y workspace son valores de configuración constantes (`server.tenant_id`, `server.workspace_id`), nunca entrada del cliente. Las operaciones Postgres se ejecutan bajo RLS forzado vía `AuthorizedContext`. |
| **Política de logging zero-secret** | Core / Servidor | Logging y telemetría en todos los paquetes | Go 1.26.5 | [`internal/config/config.go`](../internal/config/config.go), [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | Los DSN, tokens bearer, claves API, grant digests y passphrases se enmascaran estrictamente y nunca se escriben en logs ni en stdout. |
| **Eliminación y redacción de etiquetas de privacidad** | Core / Plugin de cliente | Sanitización del texto de ingesta | Go 1.26.5; `internal/domain/privacy` | [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go), [`plugin/opencode/cortex.ts`](../plugin/opencode/cortex.ts) | Redacta el contenido encerrado en etiquetas `<private>...</private>`; lo sustituye por `[REDACTED]` determinista; requiere contenido residual público. |
| **No interferencia AST (`REQ-PRIV-007`)** | Core (Zero-CGO) | Integridad estructural de la inteligencia de código | Go 1.26.5 | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go), [`AGENTS.md`](../AGENTS.md) | La inteligencia estructural del código (símbolos, firmas, resúmenes de doc, relaciones, reasoning) es metadato estructural y NUNCA se muta ni se redacta con las rutinas de privacidad. |

---

## 8. Identidad de build y procedencia del binario

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Contrato de identidad binaria** | Core (Zero-CGO) | Procedencia del ejecutable de campaña estadística | Go 1.26.5 | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (`BinaryIdentity`, línea 29) | Versión de esquema `binary-identity/v1`. Valida SHA-256 no-cero en minúsculas para binario, árbol de fuente, tool y argv. |
| **Fijación de toolchain** | Core (Zero-CGO) | Determinismo del entorno de compilación | Go 1.26.5 (`go1.26.5`) | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (línea 20), [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) | La versión del tool debe coincidir con el patrón Go portátil (`^go[0-9]+\.[0-9]+\.[0-9]+$`). CI hace cumplir `go1.26.5` exacto. |
| **Identidad de build aprobada** | Core (Zero-CGO) | Configuración de compilación reproducible | Go 1.26.5 con `-trimpath` | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (línea 23) | Fijada a `ApprovedBuildIdentity = "go-test-c-trimpath-v1"`. Los binarios construidos sin los flags aprobados fallan la validación de identidad. |
| **Vinculación del digest de publicación** | Core (Zero-CGO) | Integridad del artefacto de publicación estadística | Go 1.26.5 | [`bench/vectorhydration/provenance.go`](../bench/vectorhydration/provenance.go) (`PublicationBinding`, línea 98) | Vincula el digest de identidad binaria y el digest de identidad del protocolo. Deshabilita campos JSON desconocidos o duplicados durante el unmarshaling. |

---

## 9. Política de retención de datos y logs

| Capacidad | Estado / Tier | Alcance | Prerrequisitos | Evidencia de fuente | Limitaciones y restricciones |
|---|---|---|---|---|---|
| **Retención indefinida de artefactos (`REQ-RET-001`)** | Core / Servidor | Versiones de artefactos del Project Context Protocol | Migración 003 (SQLite) o 106 (PostgreSQL) aplicada | [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) (línea 18), [`migrations/v2/003_project_artifacts.sql`](../migrations/v2/003_project_artifacts.sql) | Revisiones, activaciones y eventos de auditoría de skills y reglas se conservan indefinidamente. El historial nunca sale del ledger. |
| **Soft delete no destructivo** | Core / Servidor | Transiciones de estado del ciclo de vida del artefacto | Persistencia SQLite o PostgreSQL | [`internal/domain/projectprotocol/doc.go`](../internal/domain/projectprotocol/doc.go) (línea 19), [`internal/domain/projectprotocol/ports_test.go`](../internal/domain/projectprotocol/ports_test.go) | El paquete NO define operaciones de hard-delete ni purge. La borrera es exclusivamente una transición de estado soft-delete. |
| **Invariante de esquema SQL sin purge** | Servidor (self-hosted single-tenant) | Triggers y grants de la base de datos PostgreSQL | PostgreSQL 16+; rol `cortex_app` | [`migrations/v2/106_project_artifacts.sql`](../migrations/v2/106_project_artifacts.sql) (línea 1567), [`internal/migration/postgres_integration_test.go`](../internal/migration/postgres_integration_test.go) | Los grants omiten explícitamente el privilegio `DELETE` en las tablas de artefactos. Los intentos de ejecutar SQL DELETE disparan el aborto de la transacción. |
| **Invariante de contabilidad monótona** | Core (Zero-CGO) | Seguimiento de uso de almacenamiento y bytes | Go 1.26.5 | [`internal/migration/v2_test.go`](../internal/migration/v2_test.go) (línea 1436) | Los totales de bytes y contadores de uso aumentan monótonamente; no pueden reiniciarse, limpiarse ni decrementarse. |

---

## 10. Matriz de privacidad y protecciones por componente

| Subsistema / Interfaz | Mecanismo de protección | Campos protegidos | Comportamiento en error | Evidencia de fuente |
|---|---|---|---|---|
| **Motor de privacidad de dominio** | Parsing exacto/insensible a mayúsculas de `<private>...</private>` y reemplazo `[REDACTED]` | Prosa de texto, campos custom, envelopes multi-campo | Rechaza etiquetas mal formadas (`ErrCodeInvalidMarker`), residual vacío (`ErrCodeRequiredEmpty`), UTF-8 inválido (`ErrCodeInvalidUTF8`) | [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **CLI (`save`, `import`, `export`)** | Sanitización de entrada y validación de residual | Títulos de observación, contenido, texto de prompt | Rechaza entradas sin contenido público residual; preserva la salida sanitizada sin loguear secretos | [`internal/cli`](../internal/cli), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Herramientas MCP (`cortex_save`)** | Filtro de privacidad pre-persistencia | Contenido de observaciones, tags, notas | Devuelve error de validación al agente llamante si las etiquetas privadas están mal formadas o el residual está vacío | [`internal/mcp/tools_memory.go`](../internal/mcp/tools_memory.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Servidor HTTP (`/api/memory`)** | Filtro de privacidad del body de la petición | Contenido de observaciones de memoria, resúmenes de sesión | HTTP 400 Bad Request con mensaje de error sanitizado sin payload | [`internal/http/server.go`](../internal/http/server.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Store Bundle (UnitOfWork)** | Cumplimiento de privacidad transaccional | Registros multi-store enlistados en el guardado atómico | Falla la transacción cerrada antes de commitear buffers sucios si la validación de privacidad falla | [`internal/store/bundle/bundle.go`](../internal/store/bundle/bundle.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Protocolo de sync (`cortex sync`)** | Preflight de privacidad por chunks | Chunks de observaciones en tránsito | Detiene la sincronización antes de la transmisión de red ante cualquier violación de privacidad | [`internal/sync/sync.go`](../internal/sync/sync.go), [`internal/domain/privacy/privacy.go`](../internal/domain/privacy/privacy.go) |
| **Inteligencia de código (AST)** | Frontera de no interferencia (`REQ-PRIV-007`) | Símbolos de código, firmas, resúmenes de doc, relaciones, reasoning | Las estructuras AST son metadato estructural del código y NUNCA se mutan ni se redactan con las rutinas de privacidad | [`internal/domain/ast/ast.go`](../internal/domain/ast/ast.go), [`AGENTS.md`](../AGENTS.md) |

---

## 11. Fixtures de benchmark sintéticos y seguros

| Fixture / Corpus | Origen y revisión | Alcance y cobertura | Garantías de seguridad | Evidencia de fuente |
|---|---|---|---|---|
| **Corpus de retrieval nativo de Cortex** | `origin: "cortex-authored-synthetic"`, `privacy_review: "synthetic-only-no-real-prompts-vendor-rows-private-data-or-secrets"` | Queries de colisión de aislamiento multi-proyecto, filtros temporales, estados de lifecycle | 100% de registros sintéticos; cero prompts de usuarios reales, cero filas de datos de vendors, cero credenciales de API, cero datos personales (PII) | [`bench/evidence/cortex-native/v1/corpus.json`](../bench/evidence/cortex-native/v1/corpus.json), [`bench/common/corpus_test.go`](../bench/common/corpus_test.go) |
| **Fixture de autoridad sintética** | Dataset de test sintético autor | Fronteras de aislamiento cross-proyecto y cross-tenant | Claves de proyecto sintéticas (`project-a`, `project-b`); verifica cero filtración cross-boundary bajo queries adversariales | [`bench/fixtures/cortex-native/authority.jsonl`](../bench/fixtures/cortex-native/authority.jsonl), [`bench/fixtures/cortex-native/authority_fixtures_test.go`](../bench/fixtures/cortex-native/authority_fixtures_test.go) |
| **Fixture de colisión sintética** | Dataset de test sintético autor | Colisión de tokens léxicos y decoys de ranking hard-negative | Evalúa la discriminación de ranking contra decoys léxicos engañosos sin pasajes propietarios ni externos | [`bench/fixtures/cortex-native/collision.jsonl`](../bench/fixtures/cortex-native/collision.jsonl), [`bench/fixtures/cortex-native/collision_fixtures_test.go`](../bench/fixtures/cortex-native/collision_fixtures_test.go) |

---

## 12. Límites de evidencia y protocolo de evaluación

| Política de evidencia | Clasificación | Reglas operativas y umbrales | Evidencia de fuente |
|---|---|---|---|
| **Gates universales de corrección** | Invariante de release obligatorio | Se permite exactamente cero violaciones de aislamiento (cualquier fuga cross-tenant o cross-project bloquea el release); coincidencia exacta con el conjunto autoritativo de stable-IDs elegibles. | [`docs/BENCHMARKS.md`](BENCHMARKS.md), [`bench/common/gates.go`](../bench/common/gates.go) |
| **Límites de ground truth de relevancia** | Clasificación de métrica | Las afirmaciones de retrieval deben evaluarse contra conjuntos de stable-IDs verificados (IDs de episodio/fact) o spans de bytes semi-abiertos. El F1 / ROUGE-L de tokens de respuesta por sí solos no pueden sustentar la relevancia del retrieval. | [`docs/BENCHMARKS.md`](BENCHMARKS.md) |
| **Límites de carga de trabajo semántica** | Frontera de revisión de código | Los cambios de lógica fuente se acotan a $\le 350$ LOC (Go/Rust/Java) o $\le 250$ LOC (TS/Python); los tests se acotan a $\le 600$ LOC en total con ficheros de test modulares $\le 250$ LOC. | [`AGENTS.md`](../AGENTS.md) |
| **Límites efímeros del agente conversacional** | Frontera arquitectónica de runtime | El agente web retiene como máximo 6 pares de turnos en memoria; se limpia al cambiar de proyecto; las trazas de retrieval son proyecciones sin contenido; las citaciones del proveedor deben resolver a handles verificados del servidor. | [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) |
| **Invariante de preregistración de gates** | Metodología estadística | Los gates de release, direcciones de métricas, tamaños de muestra y tolerancias deben preregistrarse antes de observar los resultados candidatos. La evidencia de decisión held-out nunca debe reutilizarse para calibración. | [`docs/BENCHMARKS.md`](BENCHMARKS.md), [`bench/common/gates_test.go`](../bench/common/gates_test.go) |
