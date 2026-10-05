# Arquitectura

## Modos de ejecución

`cmd/cortex/main.go` es el único punto de entrada de producción.

- El modo local es el predeterminado. `internal/cli` abre `internal/app`, que conecta SQLite, los servicios de dominio, los stores, MCP stdio, HTTP local y TUI.
- El modo servidor se selecciona con `cortex --mode server`. `internal/platform/server` aplica el esquema de PostgreSQL, crea un store autorizado y sirve HTTP autenticado más MCP Streamable HTTP.
- `cmd/cortex` es el único paquete autorizado a conectar la composición local con `internal/platform/server`.
- Los paquetes locales deben permanecer zero-CGO y no deben importar PostgreSQL, authz/identidad, Qdrant/pgvector ni la composición de servidor. `internal/app/arch_test.go` lo hace cumplir.

## Flujo de datos local

```text
cmd/cortex -> internal/cli -> internal/app -> SQLite database
                                      -> store/bundle
                                      -> domain services
                                      -> MCP stdio / local HTTP / TUI
```

`internal/domain` posee los modelos, los puertos y los servicios de negocio. `internal/store/*`
implementa la persistencia. `internal/store/bundle` coordina las escrituras locales
entre stores a través de `domain.UnitOfWork`.

## Flujo de datos del servidor

```text
HTTP or MCP request
  -> static bearer verification (constant-time compare to http.token)
  -> synthetic constant principal (configured tenant, workspace, subject, owner role)
  -> authz.AuthorizedContext
  -> postgres.AuthorizedStore operations
  -> PostgreSQL transaction bound to the constant tenant with RLS
```

Los transportes del servidor reciben solo capacidades de operación. No deben recibir
repositorios PostgreSQL en bruto, transacciones, primitivas de scoring ni autoridad
de tenant seleccionada por el cliente.

El servidor es un despliegue self-hosted single-tenant. Exactamente un principal
sintético constante se ensambla en la composición a partir de `server.principal_subject`,
`server.tenant_id` y `server.workspace_id`; el `http.token` configurado es el
único bearer estático que todo llamante debe presentar, verificado con una comparación
en tiempo constante. Tenant y workspace son constantes de configuración y nunca se derivan
de la entrada del cliente: la cabecera de petición `X-Cortex-Workspace` no puede seleccionar un
workspace, y la selección de workspace colapsa al valor por defecto configurado y fijo.
La identidad de la petición se vincula a ese principal constante, y las operaciones de
`AuthorizedStore` se ejecutan dentro de una transacción de PostgreSQL ligada al tenant
constante, donde el row level security hace cumplir el aislamiento. PostgreSQL permanece
detrás de `AuthorizedStore`; los tests de arquitectura rechazan los accesores en bruto.

## Handlers de API compartidos (`internal/api`)

Los cinco endpoints de lectura críticos para la web (`GET /api/me`, `GET /api/stats`,
`GET /api/projects`, `GET /api/agent/projects`, `GET /api/graph/project-graph`) los
sirve un único paquete de handlers neutral respecto al transporte, `internal/api`, tanto
en el mux local como en el del servidor. Solo importa `internal/domain` y la librería
estándar, de modo que ninguno de los dos mux cruza el límite arquitectónico de la pista
de servidor al consumirlo (REQ-SH-010).

Los handlers dependen de un `Port` estrecho de cinco métodos:

- `CurrentPrincipal` alimenta el sobre de identidad de `/api/me`.
- `ServerStats` alimenta los contadores de `/api/stats`.
- `Projects` alimenta la lista de identificadores de `/api/projects`.
- `AgentProjects` alimenta el corpus id/label de `/api/agent/projects`.
- `ProjectGraph` alimenta el subgrafo de `/api/graph/project-graph`.

`New(port)` vincula los handlers a un único `Port`. Los adaptadores traducen su propio
vocabulario de errores a los centinelas del paquete `ErrUnauthenticated` (401) y
`ErrForbidden` (403), de modo que los sobres emitidos permanecen idénticos en ambos mux
sin que `internal/api` importe `internal/authz`.

Dos adaptadores implementan el Port:

- **Servidor — `serverAPIPort`** (`internal/platform/server/http.go`). Envuelve las
  `Operations` con ámbito de petición, que resuelven el `AuthorizedStore`
  con ámbito de principal, y se construye por petición, de modo que ninguna operación es
  alcanzable sin un principal verificado. `translatePortError` mapea las denegaciones de
  autorización a los centinelas de la api. Su `ProjectGraph` ignora deliberadamente los
  límites `depth`/`max_nodes` del handler y reproduce el recorrido previo a la extracción
  (una lectura de proyecto de 150 filas más una consulta de subgrafo de profundidad 1 y
  30 nodos por observación) para que las respuestas sigan siendo byte-compatibles (REQ-SH-011).
- **Local — `localParityPort`** (`internal/http`). Un compuesto de `*localOpsPort`
  y `localProjectGraph`: la mitad de operaciones deriva principal, estadísticas, proyectos
  y agent projects a partir de los `bundle.Stores` de SQLite, y la mitad del grafo calcula
  `ProjectGraph` como un recorrido acotado en amplitud sobre las aristas de `graphstore`
  (profundidad por defecto 2 dentro de 1..10, `max_nodes` por defecto 100 dentro de 1..200,
  límite de semilla de 150 filas). El mux de serve registra las cinco rutas detrás del
  gate de token existente y entrega a `api.New(newLocalParityPort(deps))` este port respaldado
  por el bundle. `CurrentPrincipal` devuelve un principal local sintético de usuario único
  (`local-owner`, rol owner, permisos comodín), de modo que el cliente web observa una
  identidad real en lugar de un principal nulo (REQ-SH-012).

## Agente de proyecto conversacional

La experiencia `/agent` de la web es una funcionalidad de servidor de solo lectura. No es el
perfil `agent` de MCP y no concede a una identidad permiso para mutar Cortex. El acceso es
por capacidades: el descubrimiento de proyectos requiere búsqueda autorizada, cada resultado
de memoria está autorizado en lectura, y la evidencia de código requiere
`ResourceCode/ActionRead` para el mismo tenant, workspace y proyecto verificados. El navegador
solo puede seleccionar identificadores de proyecto devueltas por
`GET /api/agent/projects`; un identificador arbitrario o obsoleto se rechaza sin revelar
si el proyecto existe.

```text
Web /agent
  -> authenticated JSON or SSE adapter
  -> principal-derived tenant/workspace/project scope
  -> one transport-neutral internal/domain/agent service
       -> authorized hybrid memory retrieval
       -> authorized scoped AST metadata retrieval
       -> Adaptive-RAG tier selection
       -> RRF + ColBERT MaxSim fusion and reranking
       -> HippoRAG graph expansion / LightRAG community summaries
       -> one bounded CRAG refinement when confidence is low
       -> fixed prompt policy and bounded untrusted history/evidence
       -> administrator-configured hardened LLM provider
       -> server validation of citation handles
       -> canonical answer + confidence + degradation + authorized sources
```

La recuperación es adaptativa en lugar de una única búsqueda vectorial. Las preguntas
factuales directas pueden permanecer en la ruta léxica/de código más barata; las preguntas
semánticas añaden recuperación densa y fusión de rangos recíprocos; las preguntas de
relación añaden Personalized PageRank acotado sobre el grafo de memoria/código; las
preguntas arquitectónicas pueden añadir resúmenes de comunidades cacheados. La interacción
tardía estilo ColBERT reordena la evidencia candidata, y CRAG puede realizar como máximo
una reformulación local determinista. Cada etapa recibe el mismo ámbito resuelto por el
servidor y el mismo presupuesto de evidencia. Una etapa degradada o no disponible puede
reducir la confianza, pero nunca puede ampliar la visibilidad de tenant, workspace,
proyecto, clasificación o propiedad personal.

La traza pública de recuperación es una proyección libre de contenido: nivel canónico,
nombres de etapa únicos y ordenados, estado y conteos acotados, como mucho una refinación,
y códigos de degradación de una lista permitida. Las consultas, la evidencia, los
identificadores de nodos del grafo, los datos del principal, los checksums, los prompts y
los detalles del proveedor no son campos de la traza. JSON y el objeto SSE de terminal
serializan la misma respuesta canónica.

Tanto `POST /api/agent/answer` como `POST /api/agent/stream` invocan el mismo
servicio y devuelven la misma semántica de respuesta canónica. El adaptador SSE emite
`meta`, `delta`, cero o más `citation` y después `done`; los fallos posteriores al inicio
del streaming usan un evento `error` saneado. La cancelación de la petición y los plazos
propiedad del transporte se propagan a la recuperación y al proveedor.

El estado de la conversación es deliberadamente efímero. La web retiene como mucho seis
pares de turnos usuario/asistente en memoria de React y los limpia al cambiar de proyecto,
al cerrar sesión o al iniciar una conversación nueva. No persiste transcripciones en el
almacenamiento del navegador. El historial, las preguntas y el texto recuperado son datos
de prompt no confiables; ninguno puede seleccionar herramientas, un modelo, una URL de
proveedor, credenciales, tenant, workspace ni ámbito de proyecto. El puerto de completion
no tiene interfaz de mutación ni de herramientas.

La migración de PostgreSQL 109 posee el límite de AST del servidor. Sus tablas
`scoped_code_symbols`, `scoped_code_relations` y `scoped_code_index_state` usan identidades
compuestas tenant/workspace/proyecto y RLS forzado. Las filas de AST legacy de solo proyecto
nunca se rellenan retroactivamente porque su autoridad no puede reconstruirse. La evidencia
de código se limita a símbolos, firmas, resúmenes de documentación, rutas, posiciones y
relaciones; los cuerpos de los archivos fuente quedan fuera del MVP.

El servidor emite identificadores de cita opacos por petición y solo resuelve handles
presentes en el conjunto de evidencia autorizada. Las rutas, citas o destinos proporcionados
por el proveedor no son de confianza. Las cuotas y los plazos son propiedad del servidor. El
limitador actual es local al proceso, de modo que una producción escalada horizontalmente
requiere una capa de admisión compartida. Los tipos de auditoría de agente no tienen
deliberadamente campos para preguntas, historial, respuestas, evidencia, embeddings,
secretos ni URLs de proveedor; la habilitación en producción requiere un sink duradero
solo de metadatos y un registro de autorización fail-closed previo al proveedor.

## Almacenamiento y migraciones

El arranque local está dirigido por la línea base incrustada y forward-only
`migrations/v2/001_init.sql`:

1. `app.Open` sondea un fichero existente en modo lectura.
2. Las bases de datos v1, Engram, ajenas, corruptas, parciales y con checksum discordante se rechazan sin mutación.
3. La línea base v2 se aplica de forma atómica y registra su identidad SHA-256 en `cortex_meta`.
4. Las bases de datos v2 existentes deben conservar el mismo checksum de línea base.

Los ficheros raíz `migrations/001-014` son historia v1 retirada. No dirigen el arranque
local y no deben editarse como forma de cambiar el esquema v2.

El conjunto SQL incrustado está congelado. Ningún cambio edita, mueve ni borra ningún fichero
bajo `migrations/v2/`, ninguna entrada de ledger, ningún pin de checksum ni la historia raíz
retirada (REQ-SH-020). La línea local es la inmutable línea base `migrations/v2/001_init.sql`
(cuya identidad se registra en `cortex_meta`) más sus follow-ups aditivos de SQLite. La línea
de PostgreSQL ejecuta `migrations/v2/100_server.sql` hasta
`migrations/v2/112_static_bind_contract.sql`, registrando cada versión aplicada y su
SHA-256 en el ledger `cortex_server_migrations`:

- Las versiones 100-105 son byte-idénticas para siempre; sus checksums quedan fijados como
  literales históricos para que cualquier deriva sea visible en los tests unitarios en todas las plataformas.
- Las versiones 106-112 llevan pins revisados que solo se mueven con bytes revisados hasta el release.
- La versión 111 (`ServerMultiTenantVerifierSQL`) permanece incrustada pero está muerta: tras
  borrar el verificador de tokens multi-tenant retirado, sus únicas referencias son la
  declaración embed y el plumbing de migración que registra la versión, así que no tiene
  ningún llamante en runtime. El verificador
  single-tenant vivo es `cortex_verify_token_principal_v2` de la versión 110.
- La versión 112 (`static_bind_contract`) es la cabeza activa. Reemplaza aditivamente
  `cortex_bind_principal` para que el principal sintético derivado de la configuración instale
  el mismo contexto RLS de tenant/actor que uno verificado por token: la rama de procedencia
  `v1:<token>` de la migración 108 se preserva literalmente y se añade una rama `static:<hmac>`
  basada en el digest de concesión persistido del actor.
- La cabeza en runtime es 112. Un ledger que registre una versión más allá de la cabeza falla
  de forma cerrada (`ErrFutureMigration`), porque una base de datos así fue escrita por un
  runtime más nuevo. Eliminar físicamente el 111 muerto sigue siendo solo roadmap.

La línea base v2 es forward-only. No documentes ni implementes un rollback local destructivo
como ruta de upgrade normal.

## MCP

El namespace soportado es `cortex_*`. Los nombres legacy `mem_*` y el framing de Engram
son rechazados deliberadamente por los tests.

Los perfiles se definen en `internal/mcp/server.go`. Los perfiles locales soportados son
`agent` (por defecto, la suite canónica de coding agent), `dev` (la suite golden para
desarrollo de software local) y `minimal` (las herramientas de memoria centrales para
contextos de token ultra bajos):

- `agent` contiene herramientas ordinarias de memoria, grafo, scoring, revisión y proyecto.
- `dev` contiene la suite golden de herramientas de desarrollo de software (`coder` es un alias aceptado).
- `minimal` contiene solo las cinco herramientas de memoria centrales.
- `admin` (borrado destructivo y curación) y `temporal` (grafo temporal y observabilidad)
  son perfiles no agénticos obsoletos: sus claves permanecen por retrocompatibilidad pero
  están retirados del discovery estándar de agentes.

Los perfiles MCP son solo locales. El MCP local usa stdio; el MCP de servidor usa Streamable HTTP
en `/mcp` y requiere el token bearer del servidor. El dashboard web incrustado — servido
por el binario local junto a la CLI y la TUI (véase [HTTP](#http)) — es una superficie local
separada, no un perfil MCP.

## HTTP

El `cortex serve` local usa stores de SQLite y se enlaza a la dirección HTTP local configurada.
Rechaza el enlace non-loopback sin `http.token`; `/health` sigue siendo público.

El HTTP del servidor usa operaciones autorizadas de PostgreSQL. `/health` es público; `/api/*`
y `/mcp` requieren un token bearer. Los cuerpos de petición y los límites de resultado están
acotados. El dashboard web usa operaciones autorizadas de solo lectura para estadísticas del
workspace, sesiones, claves de proyecto visibles y eventos de auditoría. Las concesiones de
proyecto siguen siendo autoridad de principal del servidor y no pueden cambiarse a través del
dashboard.

## Vectores

La compilación local por defecto conecta un stub degradado `sqlite_blob` para permanecer
zero-CGO. Compila con `-tags cortex_vectors` para el escaneo coseno de BLOB de SQLite. Los
artefactos de release habilitan esta tag. Qdrant y pgvector son adaptadores externos,
solo de servidor, con tags de integración separadas.

La composición del servidor envuelve cada `VectorIndex` externo en un límite de celda inmutable.
El wrapper sobrescribe los metadatos del llamante en las escrituras y los filtros del llamante
en las lecturas con el tenant y workspace configurados, de modo que los adaptadores siguen
reutilizables para migración mientras el runtime de producción falla de forma cerrada. La
recuperación léxica de PostgreSQL sigue disponible siempre que la cobertura vectorial con ámbito
esté ausente o no sea sana. El despliegue procede por tanto en este orden: runtime/esquema
primero, reindexado no destructivo por proyecto segundo, verificación de cobertura y canario de
workspace hermano tercero. El llamante de producción es el comando síncrono
`cortex --mode server reindex --project-id <public UUID>`. Autentica el bearer administrativo
configurado, vincula tenant/workspace desde el principal sintético constante, resuelve la
identidad duradera del proyecto en PostgreSQL y registra un arranque solo de metadatos más un
resultado terminal único. No existe ningún endpoint HTTP de reindex.

El cliente de embedding del servidor es distinto del constructor local permisivo. Deriva una
lista permitida de destinos exacta de la configuración del administrador y hace cumplir los
límites de esquema, host, puerto, IP resuelta, redirección, tamaño de respuesta, timeout y
concurrencia antes de que la búsqueda híbrida o una sonda de administración con IA puedan
hacer una petición saliente. Destinos inválidos hacen fallar el arranque del servidor.

## Configuración

La configuración es YAML más overrides de entorno `CORTEX_*`. Los valores locales por defecto usan
`~/.cortex/cortex.db`. El almacenamiento del servidor tiene campos separados para:

- `server.storage.dsn`: conexión de runtime sin superusuario.
- `server.storage.migration_dsn`: conexión privilegiada de migración de esquema, requerida fuera de un bootstrap de desarrollo explícito.

La identidad del servidor es constante de configuración: `server.tenant_id`, `server.workspace_id`
y `server.principal_subject` son UUIDs requeridos que construyen el principal sintético, y
`http.token` es el bearer estático. `server.grant_digest` y `server.grant_version`
son campos obsoletos de compatibilidad; la integridad de las concesiones la calcula PostgreSQL en
`cortex_bootstrap_service_principal`. La clave eliminada `multi_tenant` es desconocida para el
esquema y se tolera en la carga sin ramificar.

Antes de abrir PostgreSQL, la composición del servidor analiza ambos DSNs y exige nombres de rol
distintos. Los DSNs pueden apuntar al mismo database. Establecer `server.bootstrap_development: true`
es la única forma soportada de omitir `migration_dsn`; este modo solo de desarrollo reutiliza el
DSN de runtime. La carga de configuración nunca sintetiza el fallback.

Nunca registres en logs DSNs, claves API, tokens bearer, digests de concesión ni otros secretos.
