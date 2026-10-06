# Despliegue de servidor

El modo servidor es la composición PostgreSQL seleccionada con
`cortex --mode server`.

## Despliegue Docker (imágenes oficiales de GHCR)

Cortex publica una imagen oficial multi-plataforma en GitHub Container Registry:
- Cortex Server (API + web UI embebida): `ghcr.io/lleontor705/cortex:latest`

El binario único sirve la web UI del operador en `/` y la API en `/api/*` en el
mismo listener; no hay imagen web separada ni un segundo puerto de UI.

```bash
# 1. Configure environment variables (optional overrides)
cp .env.example .env

# 2. Pull and start PostgreSQL and Cortex Server
docker compose up -d
```

El fichero Compose crea PostgreSQL, aplica el esquema de servidor embebido,
bootstrapa una identidad de despliegue de desarrollo (`server.tenant_id`,
`server.workspace_id`, `server.principal_subject`) y arranca Cortex Server en el
puerto `7438`, que también sirve la web UI embebida en `/` en ese mismo origen. En
el primer arranque el proceso imprime el endpoint de la web UI, la ruta del
fichero de clave y la clave de acceso web mostrada exactamente una vez; recupéralas
con `docker compose logs cortex-server` y pega la clave en
`http://localhost:7438/`. Para reconstruir localmente desde el código fuente, usa
`docker compose up --build -d`.

Para una referencia completa de todas las variables de entorno soportadas, consulta
[.env.example](../.env.example) y [CONFIGURATION.md](CONFIGURATION.md).

## Requisitos de producción

- PostgreSQL 16 o superior soportado por la política de despliegue.
- DSN de migración privilegiado y DSN de runtime no privilegiado separados.
- Constantes `server.tenant_id`, `server.workspace_id` y `server.principal_subject`
  configuradas para el principal de servicio sintético.
- Token bearer estático gestionado como secreto (`http.token`), o un gateway
  upstream autenticado que lo inyecte.
- `http.allowed_origins` explícito para clientes de navegador.
- TLS en la frontera de despliegue.

La persistencia del servidor sigue detrás de `AuthorizedStore`; los transportes
nunca reciben repositorios crudos, transacciones ni autoridad de tenant elegida
por el cliente. Cada petición autenticada es el único principal de servicio
sintético construido a partir de las constantes configuradas
`server.tenant_id`, `server.workspace_id` y `server.principal_subject`, de modo
que tenant y workspace nunca provienen de la entrada del cliente. `/health` es
público, mientras que `/api/*` y `/mcp` requieren el bearer estático.

### Frontera de roles de PostgreSQL

El arranque de servidor tipo producción requiere ambas cadenas de conexión y
valida sus nombres de rol PostgreSQL parseados antes de abrir un handle de base
de datos:

- `server.storage.dsn` / `CORTEX_SERVER_STORAGE_DSN` usa el rol `cortex_app`
  no-superusuario y de larga duración sin `BYPASSRLS`.
- `server.storage.migration_dsn` / `CORTEX_SERVER_STORAGE_MIGRATION_DSN` usa el
  rol privilegiado `cortex_migration` para las migraciones y la reconciliación de
  bootstrap en el arranque.
- Los dos roles deben ser distintos. Pueden apuntar al mismo host y base de datos
  PostgreSQL.

Los DSN y las contraseñas nunca se incluyen en errores de validación de frontera.
El runtime cierra el handle de migración antes de crear su pool de aplicación de
larga duración. Mover las migraciones y la reconciliación de bootstrap a un job
independiente one-shot sigue siendo un follow-up requerido.

Solo el desarrollo local puede fijar `server.bootstrap_development: true` y omitir
`migration_dsn`; ese switch explícito permite a Cortex reutilizar el DSN de
runtime por comodidad de migración/bootstrap. Nunca actives este fallback en
producción.

## Configuración del agente conversacional

El agente conversacional usa la configuración LLM del servidor propiedad del
administrador; los clientes no pueden enviar proveedor, modelo, URL, clave API,
lista de herramientas ni límite de salida. Configura como mínimo un proveedor y
modelo soportados, más una clave API gestionada como secreto cuando el proveedor
lo requiera:

```text
CORTEX_LLM_PROVIDER=openai
CORTEX_LLM_MODEL=gpt-4o-mini
CORTEX_LLM_API_KEY=<from-secret-manager>
```

`CORTEX_LLM_BASE_URL` es opcional para los presets de proveedor. Los destinos
personalizados deben ser URLs HTTPS absolutas sin credenciales incrustadas. La
política saliente endurecida también soporta `CORTEX_LLM_ALLOWED_HOSTS`,
`CORTEX_LLM_ALLOWED_PORTS`, `CORTEX_LLM_MAX_CONCURRENT`,
`CORTEX_LLM_MAX_REDIRECTS`, `CORTEX_LLM_MAX_RESPONSE_BODY_BYTES`,
`CORTEX_LLM_MAX_ERROR_BODY_BYTES`, `CORTEX_LLM_TIMEOUT` y `CORTEX_LLM_CA_FILE`.
Los switches de loopback `CORTEX_LLM_ALLOW_LOOPBACK` y
`CORTEX_LLM_ALLOW_LOOPBACK_HTTP` son solo para desarrollo local explícito. Una
política de destino inválida hace fallar el arranque del servidor; un proveedor
no configurado deja los endpoints del agente en fail-closed con una respuesta
sanitizada de no disponible.

### API autenticada

- `GET /api/agent/projects` devuelve solo los proyectos para los cuales el
  principal verificado tiene memoria buscable o un índice de código listo y
  legible.
- `POST /api/agent/answer` devuelve una única respuesta JSON canónica.
- `POST /api/agent/stream` devuelve eventos SSE y deshabilita el buffering de
  proxy.

Los tres endpoints requieren autenticación bearer del servidor y fijan
`Cache-Control: no-store`. Un body de petición tiene esta forma cerrada; los
campos desconocidos se rechazan:

```json
{
  "project_id": "server-issued-project-id",
  "question": "¿Qué se desarrolló para aislar los vectores?",
  "history": [
    {"role": "user", "content": "Resume el modo server"},
    {"role": "assistant", "content": "..."}
  ]
}
```

La respuesta JSON contiene `answer`, `sources` validados, `confidence` y una traza
`retrieval` sin contenido: `tier`, `stages` únicos y ordenados con status/count,
`refinement_count` opcional, códigos `degraded` de la allowlist y el conteo de
citaciones inválidas. Puede añadirse una generación de corpus de confianza cuando
el puerto de retrieval lleve una; los clientes no deben sintetizarla desde datos
de la petición ni desde checksums internos. El streaming emite `meta`, `delta`,
cero o más `citation` y un `done` terminal que lleva el mismo objeto canónico; un
fallo después de que el stream empiece lo termina con un evento `error`
sanitizado. Los reverse proxies deben preservar las desconexiones del cliente,
usar un timeout mayor de 60 segundos y deshabilitar el buffering de respuesta
para `/api/agent/stream`.

Las peticiones se acotan a una pregunta de 8 KiB, como máximo 12 mensajes de
historial (seis pares de turnos), 4 KiB por mensaje y 24 KiB de historial agregado.
El tier estándar permite actualmente 30 peticiones y 60,000 tokens de salida
reservados/reconciliados por minuto, dos peticiones concurrentes por tenant, un
default de 1,200 tokens, un hard cap de 4,096 tokens, un deadline JSON de 30
segundos y un deadline SSE de 60 segundos. El agotamiento devuelve
`429 quota_exceeded` con `Retry-After` acotado; los timeouts devuelven
`504 agent_timeout`. Estos contadores son locales al proceso, así que réplicas
múltiples deben situarse tras un limitador compartido antes del habilitado general
en producción.

Los registros de auditoría del agente deben contener solo identificadores de
correlación y de actor, tenant/workspace/proyecto, transporte, clase de resultado,
duración, conteos de tokens/fuentes, confidence y flags de degradación. Nunca
deben contener preguntas, historial, respuestas, memoria/código recuperado,
embeddings, secretos ni URLs de proveedor. El registro de autorización
pre-proveedor es obligatorio y fail-closed (`503 audit_unavailable`); la entrega
del resultado es best effort con telemetría de fallo sin contenido. Trata un
despliegue sin un sink de auditoría del agente duradero y conectado como no listo
para producción.

### Límites MVP

- Respuestas de solo lectura: sin herramientas, escrituras, acciones
  administrativas ni comandos de repositorio.
- El contexto de código son metadatos AST (símbolos, firmas, resúmenes, rutas,
  posiciones y relaciones), no cuerpos completos de ficheros fuente.
- El historial de conversación del navegador es solo memoria y se limpia en
  logout, cambio de proyecto, unmount o Nueva conversación.
- La ausencia de evidencia vectorial o de código puede producir una respuesta
  degradada desde otra evidencia autorizada. Sin evidencia autorizada devuelve un
  resultado sanitizado de no disponible sin ampliar el alcance.
- El retrieval adaptativo usa cuatro tiers acotados: directo factual, híbrido
  semántico, grafo multi-hop y global arquitectónico. El retrieval denso se fusiona
  con resultados léxicos vía RRF y se re-rankea con MaxSim; los tiers de grafo usan
  PPR estilo HippoRAG acotado y resúmenes de comunidad LightRAG. CRAG puede
  reformular localmente como mucho una vez. Un segundo resultado de baja
  confianza se abstiene con `crag_insufficient_confidence`.
- Los caches de resúmenes de comunidad tienen ámbito por identidad de tenant,
  workspace y proyecto, hacen fingerprint de los inputs de memoria y AST, y tienen
  una cardinalidad local al proceso acotada. La cancelación y los grafos no
  disponibles/truncados fallan en modo cerrado en lugar de servir resúmenes
  obsoletos.

## Orden de rollout y stage gates

El esquema del servidor está embebido en el binario
(`migrations/v2/100_server.sql` … `112_static_bind_contract.sql`). El rollout
avanza siempre en este orden exacto; cada stage tiene criterios explícitos de
avance, aborto y reintento, y ningún stage empieza antes de que el anterior avance.

### Stage 1 — Migraciones (base de datos)

1. Bootstrapar PostgreSQL 16 y crear los roles de aplicación y de autorización
   no-superusuario con `scripts/postgres/bootstrap-authz.sql` a través de un DSN
   privilegiado.
2. Configurar los DSN de runtime y migración simultáneamente, usando roles
   distintos, y después arrancar el nuevo runtime. Cada migración se aplica en
   orden estricto de versiones dentro de una transacción protegida por el lock
   `pg_advisory_xact_lock(hashtext('cortex:v2:server-migrations'))` y se registra
   en el ledger `cortex_server_migrations` con su checksum SHA-256.

- **Avanzar cuando** el ledger registre cada versión esperada (100…112) con
  checksums coincidentes, la verificación post-aplicación de la migración 109
  confirme tablas/RLS/grants con ámbito, y el arranque complete sin
  `ErrFutureMigration`.
- **Abortar cuando** cualquier preflight falle (filas huérfanas, artefactos sin
  ledger, mismatch de checksum, versión de ledger futura): la transacción hace
  rollback, el ledger no conserva ninguna entrada parcial y el runtime se niega a
  servir.
- **Reintentar** reiniciando el mismo binario: las migraciones aplicadas se
  verifican por checksum y se saltean, así que un reintento es siempre
  idempotente. Nunca edites a mano esquema ni filas del ledger para "pasar" un
  aborto.

### Stage 2 — Servidor (tráfico)

1. Confirmar que el pool de larga duración usa el DSN de runtime no privilegiado;
   el handle de migración del arranque usa el DSN de migración configurado por
   separado y se cierra antes de servir tráfico.
2. Situar el despliegue tras TLS en la frontera y conectar el bearer token
   gestionado como secreto o el gateway autenticado.

- **Avanzar cuando** `/health` responda 200 y un round-trip autenticado de
  `/api/me` devuelva el principal de servicio sintético configurado.
- **Abortar cuando** fallen la validación de arranque o los smoke checks de
  autenticación: el proceso sale en fail-closed sin servir; la base de datos nunca
  se muta en este stage.
- **Reintentar** reiniciando tras corregir la configuración; reentrar en el
  Stage 1 es innecesario porque las migraciones ya están en el ledger.

### Stage 3 — Proxies y clientes locales

1. Apuntar los clientes SQLite locales (`cortex serve`, perfiles MCP
   `agent`/`admin`/`temporal`) y cualquier proxy HTTP hacia el despliegue.
2. El HTTP local sigue rechazando bindings no-loopback sin `http.token`, y
   `app.Open` rechaza bases de datos v1/Engram/ajenas sin mutarlas.

- **Avanzar cuando** los gates locales de unit/build (`go build ./...`,
  `go test -v -count=1 ./...`) y los gates de portabilidad focalizados pasen en el
  release del cliente.
- **Abortar cuando** la sonda de compatibilidad de solo lectura rechace una base
  de datos local: mantén la base de datos local antigua intacta e investiga; la
  sonda nunca muta.
- **Reintentar** libremente; todas las comprobaciones de cliente son de solo
  lectura.

### Stage 4 — Plugins

1. Distribuir el plugin de OpenCode (`cortex setup opencode`) y el paquete de
   hooks de Claude Code.
2. Ambos plugins hablan con el servidor a través de la misma superficie HTTP
   autenticada que cualquier otro cliente.

- **Avanzar cuando** `plugin/opencode` pase `npm ci && npm test` (Vitest) y el
  harness de Claude `bash plugin/claude-code/scripts/hooks_test.sh` salga con 0.
- **Abortar cuando** el harness salga con 127 (falta bash/jq/python3/timeout):
  eso es una toolchain BLOCKED, no un defecto del plugin — corrige la toolchain y
  re-ejecuta. Los fallos HTTP del plugin se clasifican, nunca se descartan en
  silencio.
- **Reintentar** re-ejecutando el harness, que es totalmente offline/determinista.
  La entrega real de hooks NO es idempotente por sí misma: una captura
  `subagent-stop` reintentada que hace POST a `/api/observations` puede crear una
  observación duplicada, porque los reintentos no hacen deduplicación. Trata un
  reintento como seguro solo después de que la clasificación del fallo del intento
  anterior confirme que la petición nunca llegó al servidor, o después de comprobar
  la evidencia (por ejemplo, buscando la observación antes de re-enviar). Cuando la
  vía de recibo de handoff idempotente esté disponible y configurada, prefierela
  para los reintentos — las escrituras de handoff se deduplican en el servidor por
  identidad de recibo.

### Evidencia de commit por stage

Cada rollout de stage registra evidencia duradera antes de que empiece el stage
siguiente: la URL del run del workflow de CI (el ejecutor `ubuntu-latest` es
autoritativo para los gates de PostgreSQL/race/plugin), el resultado del umbral de
cobertura (≥ 70%), el head del ledger de migraciones alcanzado, y una observación
de Cortex o una nota de tarea de ForgeSpec que vincule los artefactos. El release
requiere además que el gate de aprobación manual haya observado los doce gates
del stage 1 en verde en el commit exacto que se envía.

### Rollout de vectores con ámbito de workspace

Las réplicas de vectores son opcionales y la búsqueda léxica de PostgreSQL sigue
siendo autoritativa. Lanza el aislamiento de vectores en este orden:

1. Despliega primero el runtime y el soporte de esquema/filtro del adaptador.
   Confirma que cada operación vectorial del servidor está envuelta con la
   frontera configurada de tenant y workspace y que las búsquedas seleccionadas por
   proyecto requieren el UUID público `project_id` resuelto por el servidor.
2. Reindexa un proyecto a la vez, estampando `tenant_id`, `workspace_id` y
   `project_id` en cada punto; la etiqueta del proyecto es solo metadato de
   display. No reescribas ni borres vectores legacy durante el rollout: los puntos
   sin `project_id` no pueden satisfacer una query seleccionada por proyecto y
   permanecen en fail-closed hasta ser reindexados.
3. Verifica la cobertura para ese UUID de proyecto exacto: las observaciones
   indexadas deben coincidir con el conteo autoritativo de PostgreSQL, y los
   proyectos hermanos con la misma etiqueta más los workspaces hermanos deben
   contribuir cero candidatos.
4. Solo entonces habilita o anuncia el recall semántico para ese workspace. Los
   metadatos ausentes, la cobertura incompleta, adaptadores unhealthy o fallos de
   embedding mantienen `/api/search/hybrid` en resultados léxicos autorizados.

El rollback deshabilita el proveedor de vectores o redespliega el runtime anterior
conservando las fronteras de autorización, CORS y DSN. Nunca elimina datos de
PostgreSQL ni debilita el filtrado tenant/workspace.

Ejecuta el reindex de la réplica vectorial con ámbito de forma sincrónica desde la
composición del servidor:

```bash
cortex --mode server reindex --project-id <public-project-uuid> --config /path/to/server.yaml
```

El comando verifica el `http.token` configurado a través del repositorio durable
de tokens, requiere `ResourceAdmin/ActionManage` auditado, y resuelve el UUID del
proyecto más su etiqueta canónica dentro del tenant/workspace PostgreSQL atado al
principal. No hay deliberadamente flags de override de tenant, workspace, ID
interno ni etiqueta. Rechaza proveedores de embedding/vector ausentes o unhealthy,
un corpus vacío, observaciones saltadas, o una cobertura donde los puntos
upserted no igualen al corpus autoritativo. Las filas de auditoría contienen solo
identidad de proyecto, conteos, status y duración; la cancelación detiene el
trabajo del proveedor/vector pero aun así intenta una auditoría terminal acotada.
El comando es idempotente porque la réplica externa hace upsert por ID de
observación. No expone un endpoint HTTP de larga duración y no modifica ni borra
puntos legacy.

### Rollout del agente conversacional

La migración 109 es aditiva y forward-only. Crea las tablas AST con ámbito y
revoca a `cortex_app` el acceso a los `code_symbols` y `code_relations` legacy sin
ámbito; intencionalmente no copia ninguna fila legacy. Lanza el agente en estas
ondas:

1. Aplica 109 con `cortex_migration`. Verifica el checksum del ledger, las
   foreign keys compuestas, el RLS forzado y que `cortex_app` no pueda leer el AST
   legacy.
2. Despliega el runtime consciente de 109 manteniendo el tráfico del agente
   deshabilitado en el ingress o en la capa de tenant-release. Confirma que
   `/health`, `/api/me` y las superficies de búsqueda normales siguen healthy.
3. Ejecuta el reindex AST administrativo autenticado para un proyecto canario.
   Tenant y workspace deben venir de la composición del servidor de confianza y el
   proyecto de grants duraderos; nunca acceptarlos como un mapeo SQL proporcionado
   por el operador. El job debe ser idempotente y registrar solo
   conteos/checksum/status.
4. Confirma que `scoped_code_index_state` alcance `ready`, que los conteos
   coincidan con el checkout aprobado, y que las sondas de aislamiento de
   proyecto/workspace/tenant hermanos devuelvan cero filas. Los estados `missing`,
   `indexing` o `failed` no están listos.
5. Habilita JSON para el canario, y después SSE. Verifica los handles de citación,
   la cancelación, los timeouts, el rechazo de cuota, la entrega de auditoría sin
   contenido y el streaming del proxy.
6. Reindexa y habilita los proyectos/tenants restantes solo después de que cada
   proyecto tenga evidencia de readiness equivalente.

Hasta que un proyecto esté `ready`, el AST legacy está excluido. El agente puede
responder desde la memoria autorizada y reportar degradación de código; cuando no
haya corpus autorizado disponible debe fallar sin llamar al proveedor. El
repositorio actual no proporciona un caller administrativo de producción para el
reindex AST con ámbito, así que esto sigue siendo un blocker de despliegue más que
un permiso para usar SQL directo.

El rollback deshabilita las rutas del agente en ingress/tenant release y la
ingesta AST con ámbito, y después redespliega el runtime compatible anterior si
hace falta. Conserva la migración 109 y los datos indexados. No restaures el
acceso del runtime al AST legacy, no dropees tablas con ámbito, no reescribas el
ledger, no debilites los controles RLS/CORS/SSRF, ni borres datos de
PostgreSQL/vectores.

La migración `105_workspace_binding.sql` es aditiva: añade `workspace_id` a
observaciones y recibos de handoff, backfillea estrictamente desde la cadena
durable `session -> observation -> receipt`, y aborta la transacción entera (sin
entrada parcial en el ledger) ante cualquier fila huérfana o sin resolver. Como
104, omite `IF NOT EXISTS` a propósito para que los artefactos obsoletos y sin
ledger fallen en modo cerrado en lugar de ser adoptados en silencio.

### Política de rollback

La línea de migraciones es forward-only; no existe `Down`.

- Rollback significa redesplegar el binario de runtime anterior. La migración 105
  es aditiva, así que un runtime de la era 104 sigue funcionando: sigue escribiendo
  observaciones a través del trigger `BEFORE` de compatibilidad, mientras que los
  recibos pendientes duraderos fallan intencionadamente en modo cerrado en lugar
  de aceptar un default de workspace inseguro a nivel de tenant.
- Nunca "deshagas" una migración con `DROP`/`DELETE` contra objetos de esquema o
  filas del ledger; eso bifurca la línea de migraciones y vuelve la base de datos
  inverificable.
- Nunca reescribas un checksum en `cortex_server_migrations`. Un mismatch de
  checksum falla en modo cerrado, y un ledger que registra una versión más allá del
  head del runtime (`ErrFutureMigration`, REM-ROLLOUT-001) significa que un runtime
  más nuevo es dueño de la línea: este runtime se niega a leer o escribir
  cualquier fila de migración antes de operar.

## Gates locales vs CI

Los gates dependientes de PostgreSQL (integración, e2e, aislamiento AST con ámbito
y el umbral global de cobertura de al menos 70%) fallan — no se saltan — sin
`CORTEX_TEST_POSTGRES_DSN`, `CORTEX_TEST_POSTGRES_MIGRATION_DSN` y
`CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN`. Ejecuta el gate de aislamiento del agente
con:

```bash
go test -v -count=1 -tags postgres_integration ./internal/migration ./internal/store/postgres ./internal/platform/server
```

El gate de race requiere CGO con un gcc funcional, y el harness del plugin de
Claude requiere jq; en estaciones Windows típicas estos reportan BLOCKED en local.
`.github/workflows/ci.yml` (ubuntu-latest con un servicio de PostgreSQL 16) es el
ejecutor autoritativo para ellos, y el workflow de release re-ejecuta el mismo
suite antes de su gate de aprobación manual. El gate web es
`npm test && npm run build` dentro de `web/`.
