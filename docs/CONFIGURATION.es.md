# Guía de configuración y referencia

Cortex proporciona una arquitectura de configuración unificada y multiformato que soporta **YAML**, **JSON**, **TOML** y **variables de entorno (`CORTEX_*`)**.

---

## 1. Modelo de precedencia

Los valores de configuración se resuelten estrictamente en el siguiente orden (gana la precedencia más alta):

```text
┌───────────────────────────────────────────────────────────┐
│ 1. Explicit CLI Flags (e.g. --config, --mode, --token)    │
├───────────────────────────────────────────────────────────┤
│ 2. Environment Variables (CORTEX_* / Process Environment) │
├───────────────────────────────────────────────────────────┤
│ 3. Active Configuration File (cortex.yaml, .json, .toml) │
├───────────────────────────────────────────────────────────┤
│ 4. Built-in Compiled Defaults (Zero-Bloat Baseline)      │
└───────────────────────────────────────────────────────────┘
```

* **Coincidencia de variables de entorno**: Viper traduce automáticamente las rutas con notación de puntos a snake_case en mayúsculas con el prefijo `CORTEX_` (p. ej. `llm.provider` $\rightarrow$ `CORTEX_LLM_PROVIDER`, `http.port` $\rightarrow$ `CORTEX_HTTP_PORT`).
* **Overrides de entorno**: una variable de entorno siempre anula el valor correspondiente en el fichero de configuración o en el estado de la TUI.
* **Modelo Zero-Bloat**: el gestor de configuración recorta automáticamente las secciones por defecto y vacías al escribir en disco, manteniendo los ficheros de configuración locales limpios (típicamente menos de 15 líneas) e impidiendo la serialización de pragmas internos de SQLite o de parámetros de almacenamiento específicos del servidor.

---

## 2. Métodos de configuración

### A. Ficheros de entorno (`.env` / Docker)

Copia la plantilla proporcionada para configurar contenedores, CI/CD o despliegues locales:

```bash
cp .env.example .env
```

### B. Comandos CLI de configuración

Inspecciona y actualiza claves de configuración de forma programática:

```bash
# Initialize a clean configuration file
cortex config init --format=yaml --force

# Inspect active configuration values
cortex config get ai.provider
cortex config get http.port

# Update configuration properties
cortex config set ai.provider ollama
cortex config set ai.model qwen3-embedding:8b
cortex config set http.port 7438

# Interactive setup wizard and file path lookup
cortex config wizard
cortex config path
```

### C. Gestión de autenticación por CLI

```bash
# Authenticate session with Bearer Token
cortex auth login --token=ctx_secret_token_123456

# Inspect active authentication status and subject identity
cortex auth status

# Logout and clear credentials
cortex auth logout
```

### D. Centro visual de la TUI

Lanza la Terminal User Interface interactiva:

```bash
cortex tui
```
* Pulsa `t` para cambiar dinámicamente de tema (oscuro, claro, alto contraste).
* Pulsa `L` para abrir el modal de autenticación.
* Navega hasta **Local settings** para configurar de forma interactiva los parámetros de base de datos, HTTP, MCP y replicación.

---

## 3. Referencia exhaustiva de configuración

### Infraestructura y almacenamiento del servidor

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `server.storage.driver` | `CORTEX_SERVER_STORAGE_DRIVER` | `postgres` | Driver de persistencia: `postgres` (servidor) o `sqlite` (local). |
| `server.storage.dsn` | `CORTEX_SERVER_STORAGE_DSN` | *(Ninguno)* | DSN de PostgreSQL de runtime sin privilegios (`NOSUPERUSER`, `NOBYPASSRLS`). |
| `server.storage.migration_dsn` | `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | *(Ninguno)* | DSN de migración privilegiado. Se aplica durante el preflight de arranque y se cierra inmediatamente. |
| `server.storage.max_conns` | `CORTEX_SERVER_STORAGE_MAX_CONNS` | `10` | Máximo de conexiones abiertas en el pool de base de datos. |

### Identidad y privilegios del servidor single-tenant

El modo servidor (`cortex --mode server`) ejecuta un único tenant self-hosted. Cada petición se
autentica como un único principal sintético constante ensamblado a partir de la configuración,
y el ámbito tenant/workspace es una constante de configuración que la entrada del cliente nunca
puede anular.

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `server.bootstrap_development` | `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | `false` | Modo solo de desarrollo que reutiliza el DSN de runtime cuando no se separa un rol de migración dedicado. |
| `server.tenant_id` | `CORTEX_SERVER_TENANT_ID` | *(Ninguno)* | UUID del tenant configurado (UUIDv4) vinculado al principal sintético. Requerido: el arranque falla de forma cerrada si está vacío. |
| `server.workspace_id` | `CORTEX_SERVER_WORKSPACE_ID` | *(Ninguno)* | UUID del workspace por defecto configurado (UUIDv4) vinculado al principal sintético. Requerido: el arranque falla de forma cerrada si está vacío. |
| `server.principal_subject` | `CORTEX_SERVER_PRINCIPAL_SUBJECT` | *(Ninguno)* | UUID del subject del principal sintético constante vinculado a cada petición autenticada. Requerido: el arranque falla de forma cerrada si está vacío. |
| `server.roles` | `CORTEX_SERVER_ROLES` | `[]` | Roles separados por comas para el principal configurado (el principal sintético del servidor lleva `owner`). |
| `server.scopes` | `CORTEX_SERVER_SCOPES` | `[]` | Scopes autorizados separados por comas (p. ej. `workspaces:read,workspaces:write`). |
| `server.grant_digest` | `CORTEX_SERVER_GRANT_DIGEST` | `""` | **Obsoleto.** Se conserva solo por retrocompatibilidad; la integridad de la concesión la calcula dinámicamente PostgreSQL en `cortex_bootstrap_service_principal`. |
| `server.grant_version` | `CORTEX_SERVER_GRANT_VERSION` | `0` | **Obsoleto.** Se conserva solo por retrocompatibilidad; la versión de la concesión la aprovisiona dinámicamente PostgreSQL. |

Un fichero de configuración que todavía lleve la clave eliminada `multi_tenant` se carga sin
error: la clave es desconocida para el esquema, ninguna ruta de código se ramifica por ella, y
el escritor zero-bloat la descarta en el siguiente guardado.

### API HTTP, transporte MCP y logging

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `http.enabled` | `CORTEX_HTTP_ENABLED` | `true` | Habilita la API REST HTTP (`/api/*`) y el MCP Streamable HTTP (`/mcp`). |
| `http.host` | `CORTEX_HTTP_HOST` | `localhost` | Interfaz de red a la que enlazarse (`0.0.0.0` para contenedores/todas las interfaces). |
| `http.port` | `CORTEX_HTTP_PORT` | `7438` | Puerto de escucha. *(Nota: `CORTEX_PORT` se rechaza deliberadamente).* |
| `http.token` | `CORTEX_HTTP_TOKEN` | `""` | Token Bearer estático. Autentica el HTTP/MCP local y, en modo servidor, es la única credencial verificada en cada petición antes de vincular el principal sintético constante. Requerido para enlaces non-loopback. |
| `http.allowed_origins` | `CORTEX_HTTP_ALLOWED_ORIGINS` | `[]` | Lista separada de comas de orígenes de navegador permitidos por CORS. |
| `logging.level` | `CORTEX_LOGGING_LEVEL` | `info` | Verbosidad de log: `debug`, `info`, `warn`, `error`. |
| `logging.format` | `CORTEX_LOGGING_FORMAT` | `json` | Formato de salida del log: `json`, `text`, `plain`. |

### Web UI incrustada (`web.*`)

Cortex sirve la UI de operador Next.js compilada desde el mismo binario y el mismo
escuchador HTTP que la API: no hay contenedor web separado ni un segundo puerto. El
namespace `web.*` anula esa superficie; cuando un valor no está definido hereda el
escuchador HTTP (`http.host` / `http.port`), de modo que una configuración por defecto monta
la UI donde sea que escuche la API. Consulta [embedded-web.md](embedded-web.md) para el ciclo
de vida de la clave, el formato en reposo y la semántica de rotación.

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `web.enabled` | `CORTEX_WEB_ENABLED` | `true` | Habilita la web UI incrustada. Solo un `false` explícito la deshabilita, y el valor por defecto propiedad del código nunca se escribe de vuelta en un fichero de configuración guardado. |
| `web.host` | `CORTEX_WEB_HOST` | hereda `http.host` (`localhost`) | Host del escuchador de la superficie web. Vacío hereda `http.host`; acepta una IP literal o un nombre de host simple (sin esquema, ruta, userinfo ni puerto). |
| `web.port` | `CORTEX_WEB_PORT` | hereda `http.port` (`7438`) | Puerto del escuchador de la superficie web. `0` hereda `http.port`; rango válido `1`–`65535`. |
| `web.key_file` | `CORTEX_WEB_KEY_FILE` | `~/.cortex/web.key` | Ruta al almacén de clave de acceso web incrustada. Vacío o en blanco resuelve al valor por defecto dentro del directorio de configuración de Cortex. |

La clave de acceso web es una credencia independiente con prefijo `ctx_`: nunca se usa
como `http.token`, no se deriva de él ni se le da como valor por defecto, y los endpoints
`/api/*` nunca la aceptan. En el primer arranque Cortex crea la clave e imprime el texto
plano una sola vez; después el texto plano nunca se muestra de nuevo. Géstionala con
`cortex web key show` y `cortex web key regenerate` (ambos aceptan
`--key-file PATH`).

### Configuración de IA estandarizada (separación estricta)

Cortex separa estrictamente la configuración y las credenciales de API entre **Embeddings** y **LLM** para que nunca se crucen ni se anulen entre sí sin querer:
1. **Nivel Embedding**: `CORTEX_EMBEDDING_*` / `CORTEX_EMBEDDING_API_KEY` para búsqueda semántica e indexación vectorial.
2. **Nivel LLM**: `CORTEX_LLM_*` / `CORTEX_LLM_API_KEY` para razonamiento, síntesis y extracción del agente.
3. **Acoplamiento estricto**: las variables de terceros (como `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`), las variables legacy (como `CORTEX_SEARCH_EMBEDDING_*`) y las variables unificadas `CORTEX_AI_*` **no se aceptan**. Cada subsistema requiere su propia credencial de Cortex explícita.

### Embeddings (búsqueda semántica e indexación vectorial)

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `embedding.provider` / `search.embedding_provider` | `CORTEX_EMBEDDING_PROVIDER` | *(Sin definir ⇒ embeddings desactivados)* | Preajuste del proveedor de embeddings: `none` (desactivado), `ollama`, `openai`, `openai-compatible`. Cualquier otro valor se rechaza al cargar la configuración. |
| `embedding.model` / `search.embedding_model` | `CORTEX_EMBEDDING_MODEL` | `""` (preajuste del proveedor) | Identificador del modelo de embeddings. Preajustes: `nomic-embed-text` (`ollama`), `text-embedding-3-small` (`openai` / `openai-compatible`). |
| `embedding.base_url` / `search.embedding_base_url` | `CORTEX_EMBEDDING_BASE_URL` | `""` (preajuste del proveedor) | Anulación del endpoint. Preajustes: `http://localhost:11434` (`ollama`), `https://api.openai.com/v1` (`openai`); **URL explícita obligatoria** para `openai-compatible`. |
| *(Credencial)* | `CORTEX_EMBEDDING_API_KEY` | `""` | **Credencial de API de embeddings, solo entorno.** Nunca se lee de ficheros YAML/JSON/TOML. |
| `search.ollama_auto_start` | `CORTEX_SEARCH_OLLAMA_AUTO_START` | `false` | Arranca automáticamente el daemon local de Ollama cuando se necesita. |
| `search.fusion_k` | `CORTEX_SEARCH_FUSION_K` | `60` | Constante de rango de Reciprocal Rank Fusion (RRF). |

Cuando no hay proveedor de embeddings configurado (sin definir o `none`), no se
construye ningún embedder: la búsqueda funciona solo con FTS5/BM25, el worker
durable del outbox de embeddings no se arranca y la ruta local de guardado
permanece idéntica byte a byte a una compilación sin embeddings.

### Proveedores de embeddings

`search.embedding_provider` acepta exactamente cuatro preajustes. Los valores
desconocidos fallan al cargar la configuración
(`invalid search.embedding_provider`) en lugar de desactivar embeddings en
silencio.

| Proveedor | Modos | URL base por defecto | Modelo por defecto | Credencial |
| :--- | :--- | :--- | :--- | :--- |
| `none` / *(sin definir)* | local, servidor | — | — | — |
| `ollama` | local, servidor | `http://localhost:11434` | `nomic-embed-text` (768 dims) | ninguna |
| `openai` | local, servidor | `https://api.openai.com/v1` | `text-embedding-3-small` (1536 dims) | `CORTEX_EMBEDDING_API_KEY` |
| `openai-compatible` | **solo servidor** | *(ninguna — URL explícita obligatoria)* | `text-embedding-3-small` salvo que `search.embedding_model` esté definido | `CORTEX_EMBEDDING_API_KEY` |

* **`ollama` (local por defecto)** — el proveedor de embeddings del modo local:
  se conecta al daemon en loopback y no necesita credencial.
  `search.ollama_auto_start` puede arrancarlo y `search.embedding_base_url`
  anula el endpoint.
* **`openai`** — embeddings de OpenAI sobre `{base_url}/embeddings` con
  autenticación Bearer; la clave proviene exclusivamente de
  `CORTEX_EMBEDDING_API_KEY`.
* **`openai-compatible` (modo servidor, Tier 2)** — cualquier endpoint
  compatible con OpenAI `/embeddings`. La composición local lo rechaza de forma
  fail-closed (sin embedder y sin conexión saliente); solo se construye en modo
  servidor a través de la lista blanca de la política de salida. **Requiere una
  `search.embedding_base_url` explícita** porque un endpoint genérico no tiene
  un host por defecto seguro: una URL ausente falla en la construcción
  (`embedding: openai-compatible requires an explicit base URL`).
* **`none`** — embeddings desactivados; solo BM25/FTS5 nativo.
* Las claves de API se resuelven exclusivamente en tiempo de ejecución desde
  `CORTEX_EMBEDDING_API_KEY` y nunca deben aparecer en un fichero de
  configuración.

En modo servidor, el destino se aprueba además mediante la política de salida:
HTTPS obligatorio fuera de loopback, host y puerto en lista blanca, y límites de
saltos de redirección y tamaño de respuesta.

#### Ejemplo guiado: nan.builders (endpoint compatible con OpenAI)

```yaml
search:
  embedding_provider: openai-compatible
  embedding_model: qwen3-embedding-8B
  embedding_base_url: https://api.nan.builders/v1
```

```bash
# Solo un marcador de posición — exporta la credencial real en tiempo de
# ejecución; nunca la compartas en el repositorio.
export CORTEX_EMBEDDING_API_KEY="<nan-builders-api-key>"
```

#### Dimensiones vectoriales y re-ingesta del corpus

* **Las dimensiones se reportan desde el estado vivo del servicio.** La
  dimensión se cachea a partir de la primera respuesta de embeddings, y el
  `ModelInfo` del servidor y el estado de IA leen ese valor en vivo
  (`liveEmbeddingDimensions`) en lugar de un mapa estático proveedor-dimensión.
  Antes de la primera respuesta, `ollama` reporta `768`, `openai` reporta `1536`
  y `openai-compatible` no reporta ninguna suposición (`0`), precisamente porque
  un endpoint arbitrario no tiene un valor por defecto fiable.
* **Las dimensiones que no coinciden se rechazan, nunca se almacenan.** Un
  vector cuya longitud difiera de la dimensión declarada del modelo falla con
  `domain.ErrDimensionMismatch` (`vector dimension mismatch`): ni se escribe en
  el índice vectorial ni se puntúa como cero. El worker del outbox de embeddings
  clasifica los fallos de validación como la discrepancia de dimensión como
  terminales, no reintentables, y envía la intención a la letra muerta.
* **Cambiar la dimensión del modelo exige re-ingestar el corpus.** Cambiar de un
  modelo de 768 dims (`nomic-embed-text`) a uno de 4096 dims
  (`qwen3-embedding-8B`) deja los vectores existentes en la dimensión antigua, de
  modo que cada observación debe re-embeberse mediante el worker durable del
  outbox. En la TUI, guarda la configuración de embeddings (un cambio de
  proveedor/modelo muestra la advertencia de reindexado) y pulsa `x` para
  **Reindex all embeddings**; el worker drena el outbox de forma asíncrona y
  reporta el progreso.

### Reordenamiento (reranking)

El reordenamiento reordena el conjunto de candidatos fusionados **antes** de
aplicar el límite final de resultados (post-fusión, pre-límite). Se resuelve una
vez por proceso/almacén a partir de `search.rerank_*` y está **desactivado por
defecto**: sin claves de rerank definidas, `search.rerank_provider` resuelve a
`none`, no se construye ninguna maquinaria de rerank y la salida de búsqueda es
idéntica byte a byte a una compilación sin la función.

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `search.rerank_provider` | `CORTEX_RERANK_PROVIDER` | `none` | Preajuste de rerank: `none` (desactivado), `late-interaction` (local), `openai-compatible` (solo servidor). Los valores desconocidos se rechazan al cargar. |
| `search.rerank_model` | `CORTEX_RERANK_MODEL` | `""` | Identificador del modelo de rerank (p. ej. `qwen3-Reranker-8B`). |
| `search.rerank_base_url` | `CORTEX_RERANK_BASE_URL` | `""` | URL base del endpoint. **Obligatoria** cuando `search.rerank_provider` es `openai-compatible`, y validada como destino bearer al cargar. |
| *(Credencial)* | `CORTEX_RERANK_API_KEY` | `""` | **Credencial de rerank, solo entorno.** Nunca se lee de ficheros de configuración; sin ella el reranker HTTP no puede construirse. |

* **`none` (por defecto)** — no se construye ningún reranker en ningún sitio;
  los resultados son idénticos byte a byte al orden de fusión sin rerank.
* **`late-interaction`** — el reranker local y sin red: una mezcla
  late-interaction 60% rango / 40% MaxSim sobre los vectores ya presentes en el
  almacén. Válido en modo local y en modo servidor.
* **`openai-compatible` (solo servidor)** — el reranker HTTP `/v1/rerank`.
  Requiere una `search.rerank_base_url` explícita más `CORTEX_RERANK_API_KEY`, y
  el destino se aprueba mediante la política de salida. El modo local rechaza
  construirlo (`rerank: openai-compatible is server-mode only`) y degrada a
  rerank desactivado con una advertencia en lugar de fallar la consulta.
* **Los presupuestos del proveedor se imponen en el cliente, no se asumen**: los
  documentos candidatos se envían en lotes de como máximo **32**, las peticiones
  se espacian a como máximo **60 peticiones/minuto**, las respuestas `429` se
  reintentan como máximo 3 veces, los saltos de redirección se limitan a 3 y las
  respuestas se limitan a 4 MiB.
* **Los fallos degradan, nunca corrompen**: un error de rerank, un timeout o una
  respuesta del proveedor que cambie el conjunto de candidatos conserva el orden
  de fusión sin rerank (con una advertencia) y nunca descarta ni falla una
  búsqueda.

### LLM (razonamiento, extracción y síntesis del agente)

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `llm.provider` | `CORTEX_LLM_PROVIDER` | `openai` | Proveedor generativo: `openai`, `anthropic`, `gemini`, `ollama`, `groq`, `deepseek`. |
| `llm.model` | `CORTEX_LLM_MODEL` | `gpt-4o-mini` | Identificador del modelo LLM. |
| `llm.base_url` | `CORTEX_LLM_BASE_URL` | `""` | URL base del proveedor personalizada. |
| *(Credencial)* | `CORTEX_LLM_API_KEY` | *(Secreto)* | **Credencial de API LLM estándar** (estrictamente aislada; claves de proveedor como `OPENAI_API_KEY` se ignoran). |

### Red saliente y límites de seguridad (SEC-02)

| Variable de entorno | Por defecto | Restricción / descripción |
| :--- | :--- | :--- |
| `CORTEX_LLM_PROVIDER` | `""` | Preajuste: `openai`, `anthropic`, `google`, `gemini`, `ollama`, `generic`. |
| `CORTEX_LLM_MODEL` | `""` | Anulación del identificador del modelo. |
| `CORTEX_LLM_API_KEY` | `""` | Clave de autenticación saliente. Debe proporcionarse explícitamente. |
| `CORTEX_LLM_BASE_URL` | `""` | Endpoint de destino. Debe ser HTTPS salvo que el interruptor de HTTP loopback esté habilitado. |
| `CORTEX_LLM_ALLOWED_HOSTS` | `[]` | Lista separada de comas de hostnames de destino aprobados (máx. 64). |
| `CORTEX_LLM_ALLOWED_PORTS` | `[443]` | Lista separada de comas de puertos TCP aprobados (máx. 16). |
| `CORTEX_LLM_ALLOW_LOOPBACK` | `false` | Interruptor explícito que permite destinos HTTPS loopback. |
| `CORTEX_LLM_ALLOW_LOOPBACK_HTTP` | `false` | Interruptor explícito que permite HTTP simple a loopback estricto (`127.0.0.1`, `localhost`). |
| `CORTEX_LLM_MAX_CONCURRENT` | `4` | Máximo de peticiones salientes concurrentes al proveedor (1–64). |
| `CORTEX_LLM_MAX_REDIRECTS` | `3` | Máximo de saltos de redirección HTTP permitidos (1–10). |
| `CORTEX_LLM_MAX_RESPONSE_BODY_BYTES` | `4194304` (4MB) | Tamaño máximo de payload de respuesta aceptado (hasta 64MB). |
| `CORTEX_LLM_MAX_ERROR_BODY_BYTES` | `4096` (4KB) | Payload de respuesta de error máximo retenido para diagnóstico. |
| `CORTEX_LLM_TIMEOUT` | `45s` | Timeout de la petición saliente (máx. 5m). |
| `CORTEX_LLM_CA_FILE` | `""` | Ruta a certificados raíz CA PEM personalizados para proxies TLS corporativos. |

### Adaptadores vectoriales externos (solo servidor)

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `server.provider.vector` | `CORTEX_SERVER_PROVIDER_VECTOR` | `none` | Proveedor vectorial: `none` (solo FTS5/BM25), `pgvector`, `qdrant`. |
| `vector.pgvector.dsn` | `CORTEX_VECTOR_PGVECTOR_DSN` | *(Ninguno)* | Cadena de conexión de runtime para la tabla de pgvector. |
| `vector.pgvector.schema` | `CORTEX_VECTOR_PGVECTOR_SCHEMA` | `cortex_vector` | Nombre del esquema de PostgreSQL para el almacenamiento vectorial. |
| `vector.pgvector.table` | `CORTEX_VECTOR_PGVECTOR_TABLE` | `embeddings` | Tabla que almacena los embeddings vectoriales densos. |
| `vector.pgvector.index_type`| `CORTEX_VECTOR_PGVECTOR_INDEX_TYPE` | `hnsw` | Tipo de índice vectorial: `hnsw` o `ivfflat`. |
| `vector.qdrant.host` | `CORTEX_VECTOR_QDRANT_HOST` | `localhost` | Dirección host gRPC/HTTP de Qdrant. |
| `vector.qdrant.port` | `CORTEX_VECTOR_QDRANT_PORT` | `6334` | Puerto de Qdrant. |
| `vector.qdrant.collection` | `CORTEX_VECTOR_QDRANT_COLLECTION` | `cortex` | Nombre de la colección de Qdrant de destino. |
| `vector.qdrant.api_key` | `CORTEX_VECTOR_QDRANT_API_KEY` | `""` | Clave de API de Qdrant para despliegues autenticados en la nube o en cluster. |

### Cliente local, replicación de sync y MCP remoto

| Clave de configuración | Variable de entorno | Por defecto | Descripción |
| :--- | :--- | :--- | :--- |
| `database.path` | `CORTEX_DATABASE_PATH` | `~/.cortex/cortex.db` | Ruta al fichero de base de datos SQLite local. |
| `sync.enabled` | `CORTEX_SYNC_ENABLED` | `false` | Habilita la replicación bidireccional en segundo plano de SQLite al servidor. |
| `sync.url` | `CORTEX_SYNC_URL` | `""` | URL base del Cortex Server (estrictamente HTTPS fuera de loopback). |
| `sync.token_env` | `CORTEX_SYNC_TOKEN_ENV` | `CORTEX_REMOTE_TOKEN` | Nombre de la variable de entorno que contiene el token Bearer de replicación. |
| `sync.interval` | `CORTEX_SYNC_INTERVAL` | `30s` | Frecuencia de sincronización de la replicación en segundo plano. |
| `mcp.remote.enabled` | `CORTEX_MCP_REMOTE_ENABLED` | `false` | Hace de proxy los comandos MCP stdio locales hacia un servidor remoto Streamable HTTP. |
| `mcp.remote.url` | `CORTEX_MCP_REMOTE_URL` | `""` | URL del endpoint MCP remoto (incluyendo `/mcp`). |
| `mcp.remote.token_env` | `CORTEX_MCP_REMOTE_TOKEN_ENV` | `CORTEX_REMOTE_TOKEN` | Nombre de la variable de entorno que contiene el token Bearer del MCP remoto. |

---

## 4. Política de transporte para tokens bearer

Cada destino remoto que transmita credenciales (`sync.url`, `mcp.remote.url`, `server.llm.base_url`) hace cumplir una seguridad de transporte estricta (`internal/transportpolicy`):

1. **HTTPS obligatorio fuera de loopback**: los endpoints non-loopback deben usar HTTPS. Cualquier conexión HTTP simple a una dirección pública/remota se rechaza en el arranque antes de que puedan enviarse credenciales.
2. **Excepciones de loopback estricto**: solo se permite HTTP simple para:
   * Literales de loopback IPv4 (`127.0.0.0/8`)
   * Literales de loopback IPv6 (`[::1]`)
   * El hostname exacto `localhost`
3. **Rechazo de degradación**: nunca se siguen redirecciones HTTP si degradan una conexión HTTPS a HTTP simple o cambian el origen (esquema + host + puerto).

---

## 5. Solución de problemas y diagnóstico

* **Validación de la configuración**: ejecuta `cortex doctor` para inspeccionar la salud de la configuración, la compatibilidad de la base de datos y el estado de la indexación vectorial.
* **Inspeccionar la ruta cargada**: ejecuta `cortex config path` para identificar qué fichero de configuración se está leyendo actualmente.
* **Los overrides no se reflejan**: recuerda que las variables de entorno `CORTEX_*` tienen prioridad sobre los ajustes guardados en `cortex.yaml`. Revisa las variables de entorno del proceso en ejecución.
* **Problemas de enlace de puerto**: asegúrate de usar `CORTEX_HTTP_PORT` (p. ej. `7438`). El legacy `CORTEX_PORT` se rechaza para evitar ambigüedad en la configuración.
