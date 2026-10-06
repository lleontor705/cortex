# Herramientas MCP

Cortex usa el namespace `cortex_*`. El catálogo local stdio y el del servidor Streamable HTTP son intencionalmente distintos.

## Perfiles locales

Cortex MCP está estrictamente enfocado en **capacidades agénticas** — herramientas sobre las que un agente de codificación AI puede razonar de forma autónoma, proporcionar entradas válidas y consumir dentro de su bucle cognitivo:

| Perfil | Descripción | Herramientas | Cantidad |
|---|---|---|---|
| `agent` | Suite canónica para agentes de codificación AI (por defecto) | `cortex_save`, `cortex_update`, `cortex_get_observation`, `cortex_context`, `cortex_session_summary`, `cortex_search`, `cortex_get_agent_context`, `cortex_relate`, `cortex_graph`, `cortex_graph_path`, `cortex_get_rules`, `cortex_save_rule`, `cortex_ingest_code`, `cortex_get_blast_radius`, `cortex_code_tests`, `cortex_get_code_symbols`, `cortex_detect_cycles`, `cortex_analyze_architecture`, `cortex_code_map`, `cortex_get_status`, `cortex_revision_history`, `cortex_handoff` | 22 |
| `dev` | Suite golden para desarrollo de software local (`coder` es un alias aceptado) | `cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`, `cortex_get_observation`, `cortex_get_agent_context`, `cortex_relate`, `cortex_get_rules`, `cortex_ingest_code`, `cortex_get_blast_radius`, `cortex_code_tests` | 11 |
| `minimal` | Huella ultra baja para inferencia rápida | `cortex_save`, `cortex_search`, `cortex_context`, `cortex_session_summary`, `cortex_get_observation` | 5 |

Usa `cortex mcp` (por defecto `agent`), o especifica `--tools=dev` o `--tools=minimal`. El conjunto de perfiles soportado es `agent`, `dev` y `minimal`; `admin` y `temporal` permanecen como claves obsoletas pero están retirados del discovery estándar de agentes. Las observaciones, sesiones y aristas locales usan IDs enteros; las sesiones locales usan cadenas opacas proporcionadas por el agente.

### Nota arquitectónica: retirada de herramientas no agénticas de MCP
Los toolsets `admin` (borrado destructivo, fusión de proyectos, compactación) y `temporal` (duración de ejecución, telemetría de memoria, marcas temporales RFC3339 manuales) están **obsoletos y retirados del discovery estándar de agentes**: sus entradas permanecen en `internal/mcp/server.go` para uso explícito retrocompatible, pero ninguna configuración de agente por defecto ni documentada los carga. Dado que la clave obsoleta `admin` sigue resolviendo, sus herramientas destructivas siguen siendo destructivas siempre que un llamante solicite deliberadamente `--tools=admin` (véase Seguridad más abajo).
- **¿Por qué?** Un agente autónomo nunca debe estar expuesto a operaciones destructivas (`cortex_delete`) ni se le debe pedir que registre telemetría de infraestructura (`cortex_temporal_record_operation`). Exponer más de 40 herramientas impone una enorme carga de ~6.000 tokens en el prompt y degrada la precisión en el llamado de herramientas.
- **¿A dónde fueron?**
  - Las operaciones administrativas pertenecen a la **CLI** (`cortex gc`, `cortex merge-projects`, `cortex doctor`), la **TUI** y el **Web Dashboard**.
  - La telemetría y las comprobaciones de salud pertenecen al middleware interno y al endpoint REST `/health`.
  - La evolución temporal se gestiona de forma nativa y automática mediante los upserts de `topic_key` en `cortex_save` y `cortex_revision_history`.

## Proxy remoto global

El `~/.cortex/cortex.yaml` global puede hacer que el comando stdio instalado haga proxy a un servidor Cortex publicado:

```yaml
mcp:
  enabled: true
  remote:
    enabled: true
    url: https://cortex.example/mcp
    token_env: CORTEX_REMOTE_TOKEN
    timeout: 30s
```

Define la variable de entorno indicada con un token bearer válido del servidor y reinicia el proceso del agente. En modo remoto, `cortex mcp` no abre SQLite: negocia el catálogo remoto y reenvía las llamadas/resultados de herramientas sobre el transporte stdio local. El servidor remoto controla las herramientas disponibles, así que los perfiles `--tools` locales no filtran este catálogo. El proxy falla de forma cerrada si la configuración, la autenticación o la inicialización remota fallan.

## Herramientas del servidor

El MCP del servidor expone el catálogo autenticado:

| Herramienta | Propósito | Parámetros clave |
|---|---|---|
| `cortex_save` | Guardar una observación duradera | `title`, `content`, `session_id`, `project`, `type`, `source` |
| `cortex_handoff` | Transferencia idempotente de memoria duradera | `idempotency_key`, `observation`, `relation` |
| `cortex_session_start` | Iniciar sesión de memoria | `project`, `summary` |
| `cortex_search` | Buscar observaciones (híbrido semántico + FTS5) | `query`, `type`, `project`, `scope`, `limit` |
| `cortex_get_observation` | Obtener observación por UUID público | `id` |
| `cortex_update` | Actualizar campos de una observación | `id`, `title`, `content`, `type`, `project`, `scope` |
| `cortex_delete` | Eliminar observación | `id` |
| `cortex_relate` | Crear arista semántica en el grafo | `from_id`, `to_id`, `relation_type`, `weight`, `confidence`, `reasoning` |
| `cortex_graph` | Obtener observaciones relacionadas | `observation_id`, `depth` |
| `cortex_graph_subgraph` | Obtener subgrafo heterogéneo acotado | `observation_id`, `depth`, `max_nodes` |
| `cortex_get_blast_radius` | Calcular radio de impacto y archivos afectados | `node_id`, `depth` |
| `cortex_ingest_code` | Ingerir símbolos AST y dependencias del codebase | `path`, `project`, `max_files` |
| `cortex_get_code_symbols` | Consultar símbolos AST indexados con regex | `project`, `file`, `kind`, `package`, `query`, `limit` |
| `cortex_get_code_graph` | Grafo de código estructural con callers/callees | `project` |
| `cortex_analyze_architecture` | Arquitectura completa del grafo y comunidades Louvain | `project` |
| `cortex_detect_cycles` | Detectar dependencias circulares (SCC de Tarjan) | `project` |
| `cortex_get_agent_context` | Paquete de contexto estructurado para prompts de agente | `project`, `format`, `max_tokens` |
| `cortex_get_compact_context` | Paquete de contexto de prompt acotado y de alta densidad | `project`, `max_tokens` |
| `cortex_score` | Obtener la puntuación de importancia de una observación | `observation_id` |
| `cortex_get_project_context` | Obtener gobernanza corporativa y reglas de proyecto | `project` |
| `cortex_list_skills` | Listar skills corporativas y de proyecto | `project` |
| `cortex_get_skill` | Obtener instrucciones y reglas de una skill por clave | `key`, `project` |
| `cortex_resolve_query` | Resolver consultas de forma inteligente en modo Server | `query`, `project`, `limit` |
| `cortex_get_status` | Modo operativo (Server PostgreSQL) y capacidades | - |

Las herramientas del servidor usan UUIDs públicos y operan a través de `AuthorizedStore`. `cortex_graph_subgraph` devuelve la proyección heterogénea y acotada de observaciones, entidades, actores, sesiones y proyectos. Las capacidades solo-REST del servidor (estadísticas, sesiones, proyectos y auditoría) se documentan en [HTTP-API.md](HTTP-API.md).

Los agentes deben usar el esquema devuelto por `tools/list`: los IDs numéricos de un catálogo local no son intercambiables con los UUIDs del servidor. Cambiar `mcp.remote.enabled` cambia tanto el catálogo como su esquema de IDs.

## Seguridad

`cortex_delete` es destructivo en el perfil local obsoleto `admin` (alcanzable solo solicitando explícitamente `--tools=admin`) y es solo soft-delete en el subconjunto actual del servidor. Revisa `tools/list` para conocer el catálogo y el esquema exactos de cada transporte. Los nombres de perfil/herramienta desconocidos deben tratarse como errores de configuración, no asumirse disponibles.
