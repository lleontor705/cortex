# API HTTP

## Autenticación

El HTTP local protege los bindings no-loopback con `http.token`, aceptado como
`Authorization: Bearer <token>` o `X-API-Key: <token>`. El modo servidor requiere
`Authorization: Bearer <token>` para `/api/*` y `/mcp`, donde el bearer es el
único token estático configurado. `/health` es público. Los clientes de navegador
deben usar un origen listado en `http.allowed_origins`.

El modo servidor deriva el principal de la petición desde el bearer verificado
contra una identidad sintética single-tenant ensamblada desde las constantes de
despliegue `server.tenant_id`, `server.workspace_id` y `server.principal_subject`.
La autoridad de tenant y workspace nunca se acepta desde la entrada del cliente:
ninguna cabecera de petición selecciona un tenant o un workspace.

## Rutas de paridad

Cinco rutas críticas para la web se sirven con envelopes JSON idénticos por los
handlers neutrales de `internal/api` en **ambos** mux:

| Método | Ruta | Propósito |
|---|---|---|
| `GET` | `/api/me` | Principal actual |
| `GET` | `/api/stats` | Contadores de observaciones, sesiones y proyectos |
| `GET` | `/api/projects` | Claves de proyecto visibles |
| `GET` | `/api/agent/projects` | Descriptores de proyecto del agente |
| `GET` | `/api/graph/project-graph` | Grafo de código y conocimiento del proyecto acotado |

- **Modo servidor**: registrado detrás del middleware de autenticación `Bearer`
  estático y respaldado por `AuthorizedStore`. `/api/me` renderiza el principal
  de servicio sintético configurado.
- **`serve` local**: registrado detrás del gate `http.token` y respaldado por los
  stores del bundle local. `/api/me` renderiza un principal sintético de
  propietario local (modo single-user) en lugar de un principal null.

## SQLite local

El HTTP local está implementado en `internal/http`. Los IDs de observación, prompt
y edge son enteros locales. Los IDs de sesión son cadenas opacas proporcionadas por
el agente. Estos identificadores locales no son UUID del servidor.

| Método | Ruta | Propósito |
|---|---|---|
| `GET` | `/health` | Salud del proceso local; siempre público |
| `GET/POST` | `/api/sessions` | Listar o crear sesiones locales |
| `GET` | `/api/sessions/{id}` | Leer una sesión por ID opaco del agente |
| `POST` | `/api/sessions/{id}/end` | Terminar una sesión local |
| `POST` | `/api/prompts` | Guardar la entrada del usuario en `user_prompts` |
| `GET/POST` | `/api/observations` | Listar o crear observaciones |

`POST /api/prompts` acepta `session_id`, `content` y `project`. El acceso desde
navegador usa orígenes exactos de `http.allowed_origins`; CORS no sustituye la
autenticación por token de API.

## Rutas exclusivas del servidor diferidas en modo local

Las siguientes rutas administrativas las sirve solo el mux del servidor. **No están
registradas** en el mux local `serve` y caen a `404` allí; cada una es roadmap para
modo local con su prerrequisito indicado.

| Método | Ruta | Propósito | Estado local |
|---|---|---|---|
| `GET` | `/api/audit` | Entradas de auditoría solo admin | Diferido — necesita un store de auditoría local |
| `GET` | `/api/system/metrics` | Métricas de sistema | Diferido — necesita un colector de métricas |
| `GET` | `/api/rag/stats` | Estadísticas del subsistema RAG | Diferido — necesita estadísticas RAG locales |

## PostgreSQL de servidor

El modo servidor expone solo fronteras de operación autorizadas y UUIDs públicos:

| Método | Ruta | Propósito |
|---|---|---|
| `GET` | `/health` | Salud de la base de datos |
| `GET` | `/api/observations` | Listar observaciones |
| `POST` | `/api/observations` | Crear observación |
| `GET` | `/api/observations/{uuid}` | Leer observación |
| `PUT` | `/api/observations/{uuid}` | Actualizar observación |
| `DELETE` | `/api/observations/{uuid}` | Borrar observación |
| `POST` | `/api/sessions` | Crear sesión |
| `GET` | `/api/sessions` | Listar sesiones autorizadas |
| `GET` | `/api/search` | Buscar observaciones |
| `POST` | `/api/graph/edges` | Crear edge de grafo |
| `GET` | `/api/graph/{uuid}/related` | Observaciones relacionadas |
| `GET` | `/api/graph/{uuid}/subgraph` | Subgrafo heterogéneo |
| `GET` | `/api/graph/project-graph` | Grafo completo de código y conocimiento del proyecto |
| `GET` | `/api/graph/analytics` | Informe de salud estructural (comunidades Louvain, God nodes, ciclos) |
| `GET` | `/api/graph/blast-radius` | Cálculo de impacto de blast radius |
| `POST` | `/api/graph/ingest-code` | Ingerir entidades de código AST en el grafo del proyecto |
| `POST` | `/api/graph/resolve` | Resolución dinámica de conflictos (`supersedes`) |
| `DELETE` | `/api/graph/edges/{uuid}` | Borrar edge de grafo |
| `GET` | `/api/scores/{uuid}` | Score de importancia |
| `GET` | `/api/stats` | Contadores del workspace |
| `GET` | `/api/projects` | Claves de proyecto visibles |
| `GET` | `/api/projects/context` | Reglas corporativas y system prompt |
| `GET` | `/api/projects/artifacts` | Listar artefactos del proyecto |
| `POST` | `/api/projects/artifacts` | Guardar artefacto del proyecto |
| `DELETE` | `/api/projects/artifacts/{id}` | Borrar artefacto del proyecto |
| `GET` | `/api/audit` | Entradas de auditoría solo admin |
| `GET` | `/api/system/metrics` | Métricas de sistema |
| `GET` | `/api/rag/stats` | Estadísticas del subsistema RAG |
| `POST` | `/mcp` | MCP Streamable HTTP |

La autoridad de tenant/workspace de la petición nunca se acepta de los clientes.
Los metadatos de edge del servidor se limitan a los campos persistidos por el
esquema PostgreSQL.
