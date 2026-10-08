# Referencia de la CLI de Cortex

Este es el único contrato autoritativo para el binario de línea de comandos
`cortex` (`cmd/cortex`). Cada comando, flag, default, sobrescritura por variable de
entorno, código de salida y requisito de autenticación documentado aquí está
anclado a la implementación entregada: `cmd/cortex/main.go`,
`internal/cli/cli.go`, `internal/cli/web.go`, `internal/cli/watch.go`,
`internal/platform/mode.go` y `internal/config`.

Si este documento y cualquier otro documento del repositorio discrepan sobre el
comportamiento de la CLI, gana este documento. Las tablas de flags deben existir en
exactamente un lugar, y ese lugar es aquí.

> **Referencias relacionadas.** Las claves de configuración y formatos de fichero
> viven en [CONFIGURATION.md](CONFIGURATION.md). Los catálogos de herramientas y
> perfiles de MCP viven en [MCP.md](MCP.md). El despliegue del servidor vive en
> [SERVER.md](SERVER.md). La superficie REST HTTP vive en
> [HTTP-API.md](HTTP-API.md). Las pantallas de terminal interactivas viven en
> [TUI-GUIDE.md](TUI-GUIDE.md).

Secciones: [modelo de invocación](#1-modelo-de-invocacion),
[convenciones globales](#2-convenciones-globales),
[referencia de comandos](#3-referencia-de-comandos),
[índice de variables de entorno](#4-indice-de-variables-de-entorno),
[matriz de autenticación](#5-matriz-de-autenticacion) y
[superficie obsoleta](#6-superficie-obsoleta-y-retirada).

---

## 1. Modelo de invocación

### 1.1 Binario y orden de argumentos

El punto de entrada de producción es `cmd/cortex` (el binario `cortex`). La forma
general es:

```text
cortex [--mode local|hybrid|server] <command> [arguments...]
```

El binario primero extrae y elimina el flag global `--mode` y después despacha sobre
el siguiente argumento posicional (`args[1]`). Se aceptan ambas sintaxis de flag y el
flag puede aparecer antes o después del subcomando:

```text
cortex --mode hybrid search "release notes"
cortex --mode=hybrid search "release notes"
cortex search "release notes" --mode=hybrid
```

El parsing de `--mode` es estricto. El conjunto válido es exactamente `local`,
`hybrid` y `server`; cualquier otro valor falla en modo cerrado con código de salida
`2` y el mensaje de error:

```text
cortex: unknown mode "…": use --mode local, --mode hybrid, or --mode server
```

Cuando `--mode` está ausente el modo efectivo es `local`.

### 1.2 La tríada de modos

| Modo | Backend | Superficie de red | Notas |
|---|---|---|---|
| `local` | SQLite de binario único (`~/.cortex/cortex.db`) con MCP stdio local | Ninguna requerida | Default. Byte-compatible con la composición local independiente. |
| `hybrid` | SQLite local más el bucle de sync/replicación remoto | Endpoint de sync remoto | No es un store separado: es la composición local con la replicación encima. El binario exporta `CORTEX_MODE=hybrid` para el proceso. |
| `server` | PostgreSQL single-tenant sobre HTTP autenticado y MCP Streamable HTTP | Listener HTTP + `/mcp` | Conectado por `cmd/cortex`; ver [`--mode server`](#17-mode-server). |

`cortex status` (alias `cortex mode`) reporta la tríada activa y su descripción. El
PostgreSQL en modo servidor es single-tenant; el ámbito de tenant y workspace son
constantes de configuración, nunca entrada del cliente.

### 1.3 Contrato de códigos de salida

| Código | Significado | Condiciones productoras |
|---|---|---|
| `0` | Éxito. | El comando completó e imprimió su resultado. Los comandos que no encuentran datos coincidentes (por ejemplo una búsqueda vacía) aun así salen con `0`. |
| `1` | Fallo. | Un error de runtime (base de datos, red, filesystem), un fallo de validación, un comando desconocido, o un error de argumentos/uso por comando (argumentos requeridos ausentes, flag numérico inválido, subcomando inválido). Ejecutar `cortex` sin comando también sale con `1`. |
| `2` | Error de uso/argumentos en la capa de invocación. | Un valor `--mode` inválido, o un fallo de argumento/configuración de `--mode server`: argumento de servidor desconocido, `--config` sin ruta, `reindex --project-id` inválido, config de servidor que falla al cargar, o fallo de bootstrap/web-key del servidor. |

```bash
# exit 0: a successful search
cortex search "auth tokens"

# exit 1: missing search query
cortex search
echo $?   # 1

# exit 2: invalid mode
cortex --mode bogus search "x"
echo $?   # 2
```

> **Nota.** Los errores de uso por comando (por ejemplo `cortex save` sin title y
> content, o `cortex config` sin subcomando) devuelven actualmente `1`, no `2`. Solo
> el parsing de modos de primer nivel y la capa de invocación del servidor usan `2`.

### 1.4 Invocación interactiva desnuda → TUI

Cuando el modo efectivo es `local` o `hybrid` y el proceso se invoca de forma
interactiva sin argumentos de comando:

- el caller es una terminal interactiva (`stdout` es un TTY y `stdin` es un TTY), y
- no hay argumentos de comando,

entonces el binario reescribe la invocación a `cortex tui` e lanza la interfaz de
terminal interactiva en lugar de imprimir el uso. Esto cubre el doble clic en el
binario en Windows o ejecutar `cortex` directamente en una terminal. Las
invocaciones no interactivas (pipes, scripts, CI) siguen imprimiendo el uso y salen
con `1`.

### 1.5 Aviso de actualización en segundo plano

Cada intento de dispatch excepto `help` inicia una comprobación de actualización en
segundo plano. El resultado se imprime en **stderr**, nunca en stdout, para que no
pueda corromper la salida legible por máquina:

```text
A new version of cortex is available: vX.Y.Z (current: vA.B.C)
<release-url>
```

- `cortex version` imprime el avance de forma síncrona.
- Otros comandos drenan el canal en segundo plano de forma non-blocking justo antes
  de salir, así que el aviso aparece solo cuando la comprobación ya terminó (best
  effort).
- `cortex update --check` realiza una comprobación síncrona en su lugar.

### 1.6 `cortex serve` versus `--mode server`

`cortex serve` arranca la API REST HTTP SQLite **local** y la web UI embebida en el
listener configurado (`http.host`/`http.port`, default `localhost:7438`). No es el
servidor PostgreSQL.

`cortex --mode server` arranca la composición **PostgreSQL single-tenant** con
`/api/*` autenticado y MCP Streamable HTTP en `/mcp`, más la web UI embebida. Ver
[`--mode server`](#17-mode-server).

### 1.7 `--mode server`

```text
cortex --mode server [options]
cortex --mode server reindex --project-id <uuid> [options]
```

| Argumento | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--config <path>` | *(ninguno)* | — | Ruta al fichero YAML de configuración del servidor. También acepta `--config=<path>`. |
| `reindex` | off | — | Ejecuta reindexado síncrono offline para un proyecto y después sale. |
| `--project-id <uuid>` | *(requerido con reindex)* | — | UUID público del proyecto a reindexar. Debe ser un UUID bien formado no-nulo. |
| `-h`, `--help`, `help` | — | — | Imprime la ayuda del servidor y sale con `0`. |
| `-v`, `--version`, `version` | — | — | Imprime la versión del binario y sale con `0`. |

El arranque del servidor lee la configuración desde `--config` (o las rutas de
búsqueda por defecto), abre la composición PostgreSQL, acuña o carga la clave de
acceso web embebida e imprime el banner del endpoint:

```text
cortex: server endpoint http://<host>:<port>
cortex: readiness http://<host>:<port>/health
cortex: API http://<host>:<port>/api/
cortex: MCP http://<host>:<port>/mcp
cortex: web http://<host>:<port>/
cortex: web key file <path>
```

En el primer arranque la clave de acceso web plaintext se imprime exactamente una
vez. `reindex` imprime una única línea de resumen:

```text
cortex: server reindex complete project=<uuid> corpus=N upserted=N reembedded=N skipped=N batches=N
```

Códigos de salida: `0` éxito; `2` fallo de argumento/configuración/bootstrap; `1`
fallo de runtime (error de `rt.Serve`) o error de ejecución del reindex.

Autenticación: el servidor verifica el bearer estático configurado
(`http.token` / `CORTEX_HTTP_TOKEN`) en cada petición `/api/*` y `/mcp` antes de
atarse al principal sintético constante. `/health` no está autenticado. La web UI
embebida usa la clave de acceso web separada.

---

## 2. Convenciones globales

### 2.1 Estilos de flag

- **Flags con valor** aceptan ambas formas: `--limit 20` y `--limit=20`. La mayoría
  de comandos implementan la forma con espacio; `--mode`, `--tools`, `--profile` y
  los subcomandos `code` también aceptan la forma `=`. Cuando un flag con valor se
  usa sin valor, el parser lo ignora o cae a su default en lugar de consumir el
  siguiente token como valor.
- **Flags booleanos** son solo-de-presencia (`--json`, `--graph-expand`,
  `--staged`, `--regex`, `--pack`, `--dry-run`, `--check`, `--include-personal`,
  `--force`, `--to-obsidian`, `--remote`). No existe la forma `--flag=false` para
  ellos.
- **Aliases.** Varios comandos aceptan alias cortos: `-p` (`--project`),
  `-m` (`--max-files`), `-f` (`--file`/`--force`, según contexto),
  `-t` (`--token`), `-k` (`--key-file`), `-c` (`--check`), `-o` (`--output`).
- Los tokens desconocidos que no empiezan por `-` se tratan como argumentos
  posicionales (una query, una ruta, un target). Los tokens desconocidos que sí
  empiezan por `-` se ignoran en la mayoría de los parsers en lugar de rechazarse.

### 2.2 Defaults

Los defaults de flags documentados en esta referencia son los defaults efectivos de
la CLI en `internal/config`/`internal/cli`. Son independientes de los defaults de
configuración: por ejemplo `cortex search` usa un límite de resultados hard-coded de
`10`, mientras que la clave de configuración `search.default_limit` tiene default
`20`. Cuando existen ambos, se aplica el default de la CLI a menos que se provea el
flag.

### 2.3 Preflight de privacidad

`cortex save` y `cortex import` ejecutan un preflight de privacidad antes de
persistir:

- Todos los campos de metadata (`project`, `topic_key`, `scope`, `type`, `source`,
  `session_id` y tags) se validan con `privacy.ValidateMetadata`.
- `title` y `content` pasan por `privacy.ProtectField`.
- Para import por batch, la petición entera se staging y valida contra copias
  defensivas; si cualquier registro falla, el import se rechaza atómicamente con
  salida `1` y no se escribe nada.

### 2.4 Espacios de identificadores

- **Modo local** usa IDs de observación enteros (por ejemplo `#42`), cadenas de
  sesión opacas proveídas por el agente y IDs de grafo enteros.
- **Modo servidor** usa UUIDs públicos para observaciones y proyectos.
- Los dos espacios no son intercambiables. Los IDs numéricos de un catálogo local
  nunca deben pasarse a endpoints del servidor ni viceversa.

### 2.5 Streams de salida

- Los resultados primarios van a **stdout**.
- Errores, warnings y el aviso de actualización en segundo plano van a **stderr**.
- `cortex serve` emite un banner `key=value` seguro para pipes para que su salida
  siga siendo legible por máquina.

---

## 3. Referencia de comandos

Inventario de comandos de un vistazo:

| Comando | Propósito |
|---|---|
| [`help`](#31-help) | Imprimir el uso |
| [`version`](#32-version) | Imprimir la versión del binario |
| [`tui`](#33-tui) | Lanzar la interfaz de terminal interactiva |
| [`serve`](#34-serve) | Arrancar la API HTTP SQLite local + web embebida |
| [`mcp`](#35-mcp) | Arrancar el servidor MCP stdio local |
| [`search`](#36-search) | Buscar observaciones |
| [`save`](#37-save) | Guardar una observación |
| [`context`](#38-context) | Mostrar el contexto de sesión reciente o un context pack |
| [`stats`](#39-stats) | Mostrar estadísticas de memoria |
| [`timeline`](#310-timeline) | Mostrar contexto cronológico alrededor de una observación |
| [`revisions`](#311-revisions) | Mostrar el historial de revisiones de una observación |
| [`setup`](#312-setup) | Instalar integraciones de agente / configurar Ollama |
| [`import`](#313-import) | Importar observaciones desde JSON |
| [`export`](#314-export) | Exportar observaciones a JSON u Obsidian |
| [`sync`](#315-sync) | Sincronizar vía chunks de fichero o replicación remota |
| [`merge-projects`](#316-merge-projects) | Fusionar variantes de nombre de proyecto |
| [`reindex`](#317-reindex) | Reconstruir embeddings vectoriales |
| [`doctor`](#318-doctor) | Ejecutar health checks |
| [`gc`](#319-gc) | Garbage-collect de observaciones archivadas |
| [`backup`](#320-backup) | Crear un snapshot atómico de SQLite |
| [`watch`](#321-watch) | Watcher de ficheros continuo para indexado AST |
| [`ingest`](#322-ingest) | Ingesta AST única |
| [`code`](#323-code) | Inteligencia AST de código (9 subcomandos) |
| [`config`](#324-config) | Gestionar configuración |
| [`auth`](#325-auth) | Gestionar el token de autenticación local |
| [`web`](#326-web) | Gestionar la credencial web embebida |
| [`status` / `mode`](#327-status-mode) | Mostrar modo operativo y estado |
| [`migrate`](#328-migrate) | Gestionar migraciones de base de datos |
| [`update`](#329-update) | Actualizar Cortex al último release |

### 3.1 `help`

**Sinopsis**

```text
cortex help
cortex --help
cortex -h
```

Imprime la lista de uso/comandos de primer nivel en stdout y sale con `0`.

**Argumentos:** ninguno. **Flags:** ninguno. **Códigos de salida:** `0`.
**Autenticación:** ninguna.

**Ejemplo**

```bash
cortex help
```

**Ver también:** [version](#32-version).

### 3.2 `version`

**Sinopsis**

```text
cortex version
cortex --version
cortex -v
```

Imprime `cortex <version>` en stdout, drena el aviso de actualización en segundo
plano y sale con `0`. La versión la inyecta GoReleaser vía ldflags; cuando se
construye desde fuente cae a la versión del módulo, o `dev`.

**Argumentos:** ninguno. **Flags:** ninguno. **Códigos de salida:** `0`.
**Autenticación:** ninguna.

**Ejemplo**

```bash
cortex version
# cortex v2.4.0
```

**Ver también:** [update](#329-update).

### 3.3 `tui`

**Sinopsis**

```text
cortex tui [--config]
```

Lanza la interfaz de terminal interactiva Bubble Tea.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--config` | off | — | Abrir directamente la pantalla del Configuration Center en lugar del dashboard. |

**Argumentos:** ninguno. **Códigos de salida:** `0` éxito, `1` en error de
app-open o de programa. **Autenticación:** ninguna (store local).

**Ejemplos**

```bash
cortex tui
cortex tui --config
```

Una invocación interactiva desnuda de `cortex` se reescribe a `cortex tui`
(ver [§1.4](#14-invocacion-interactiva-desnuda-tui)).

**Ver también:** [TUI-GUIDE.md](TUI-GUIDE.md), [`config wizard`](#324-config).

### 3.4 `serve`

**Sinopsis**

```text
cortex serve
```

Arranca la API REST HTTP SQLite local (`/api/*`) y, cuando la superficie web se
monta, la web UI embebida. La dirección de escucha es `http.host:http.port`.

`serve` no toma flags; los argumentos finales se ignoran.

| Ajuste | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `http.host` | `localhost` | `CORTEX_HTTP_HOST` | Interfaz de escucha. Vacío/`localhost`/loopback se trata como local. |
| `http.port` | `7438` | `CORTEX_HTTP_PORT` | Puerto de escucha. |
| `http.token` | *(vacío)* | `CORTEX_HTTP_TOKEN` | Bearer estático. Requerido para binds no-loopback. |
| `http.allowed_origins` | `[]` | `CORTEX_HTTP_ALLOWED_ORIGINS` | Orígenes de navegador CORS. |

En el primer arranque la clave de acceso web se acuña e imprime exactamente una
vez. El banner:

```text
mode=local
store=<database path>
sync=disabled|enabled
web=mounted|disabled
key_file=<path>
listen=<host>:<port>
```

**Códigos de salida:** `0` éxito; `1` si la app falla al abrir, el bind no-loopback
no tiene `http.token`, la clave web no puede resolverse, o el listener falla.
**Autenticación:** ninguna por defecto; cuando `http.token` está fijado, `/api/*` y
el MCP montado requieren `Authorization: Bearer <token>`. `/health` sigue sin
autenticar.

**Ejemplos**

```bash
cortex serve
CORTEX_HTTP_TOKEN=secret CORTEX_HTTP_HOST=0.0.0.0 cortex serve
```

**Ver también:** [HTTP-API.md](HTTP-API.md), [web key](#326-web),
[`--mode server`](#17-mode-server).

### 3.5 `mcp`

**Sinopsis**

```text
cortex mcp [--tools=PROFILE] [--profile=PROFILE]
```

Arranca el servidor MCP stdio local.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--tools[=]PROFILE` | `mcp.profile` (default `agent`) | `CORTEX_MCP_PROFILE` | Catálogo de herramientas: `agent` (22 tools), `dev` (11 tools), `minimal` (5 tools). |
| `--profile[=]PROFILE` | *(igual que `--tools`)* | `CORTEX_MCP_PROFILE` | Alias de `--tools`. |

Cuando `mcp.remote.enabled` es true, `cortex mcp` no abre SQLite; hace de proxy del
servidor remoto configurado y reenvía las llamadas sobre el transporte stdio local.
En ese modo el servidor remoto es dueño del catálogo y los perfiles locales no lo
filtran.

**Códigos de salida:** `0` éxito; `1` en fallo de carga de config, app-open o
serve. **Autenticación:** ninguna localmente (stdio con ámbito de proceso). Un
proxy remoto lee su bearer de `mcp.remote.token_env` (default
`CORTEX_REMOTE_TOKEN`).

**Ejemplos**

```bash
cortex mcp
cortex mcp --tools=minimal
cortex mcp --profile dev
```

Los toolsets `admin` y `temporal` están retirados; ver
[§6](#6-superficie-obsoleta-y-retirada).

**Ver también:** [MCP.md](MCP.md), [`config`](#324-config).

### 3.6 `search`

**Sinopsis**

```text
cortex search <query> [flags]
```

Busca observaciones con retrieval multi-modal.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `<query>` | sí | Texto de la query. Varios tokens que no son flags se unen con espacios. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--mode[=]MODE` | `auto` | — | Modo de retrieval: `auto`, `direct`, `semantic`, `multi_hop`. `multi_hop` habilita la expansión de grafo; `direct` la deshabilita. |
| `--graph-expand` | off | — | Forzar la expansión de grafo activada. |
| `--type TYPE` | *(vacío)* | — | Filtrar por tipo de observación. |
| `--project PROJECT` | *(vacío)* | — | Filtrar por proyecto. |
| `--scope SCOPE` | *(vacío)* | — | Filtrar por scope. |
| `--limit N` | `10` | — | Resultados máximos. Debe ser un entero; los valores inválidos salen con `1`. |

**Códigos de salida:** `0` éxito (incluyendo cero coincidencias, que imprime
`No memories found for: "<query>"`); `1` por query ausente/en blanco, `--limit`
inválido, o error de store. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex search "release process"
cortex search "graph ranking" --mode=multi_hop --limit 5
cortex search "auth" --type decision --project cortex --scope project
```

**Ver también:** [context](#38-context), [CONFIGURATION.md](CONFIGURATION.md).

### 3.7 `save`

**Sinopsis**

```text
cortex save <title> <content> [flags]
```

Guarda una observación local tras el preflight de privacidad.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `<title>` | sí | Título de la observación. Debe ser no-blanco. |
| `<content>` | sí | Cuerpo de la observación. Debe ser no-blanco. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--type TYPE` | `manual` | — | Tipo de observación. |
| `--project PROJECT` | *(vacío)* | — | Nombre del proyecto. |
| `--scope SCOPE` | `project` | — | Scope de la observación. |
| `--topic TOPIC_KEY` | *(vacío)* | — | Clave de tema estable para dedupe/upsert. |

El session ID usa por defecto `manual-save` (o `manual-save-<project>` cuando se da
un proyecto).

**Códigos de salida:** `0` éxito; `1` en rechazo de validación/privacidad o error
de store. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex save "Release checklist" "Tag, build, publish, announce"
cortex save "Auth model" "Static bearer only" --type decision --topic architecture/auth-model --project cortex
```

**Ver también:** [import](#313-import), preflight de privacidad
[§2.3](#23-preflight-de-privacidad).

### 3.8 `context`

**Sinopsis**

```text
cortex context [project] [flags]
```

Muestra el contexto de sesión reciente, o renderiza un context pack cuando se
selecciona un formato.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `[project]` | no | Filtro de proyecto. El primer token que no es flag ni valor de flag. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--scope SCOPE` | *(vacío)* | — | Filtrar por scope. |
| `--format FORMAT` | *(vacío)* | — | Renderizar un context pack en el formato dado (por ejemplo `markdown`, `json`). También acepta `--format=FORMAT`. |
| `--pack` | off | — | Atajo: seleccionar el formato de pack `markdown`. |
| `--budget=N` | `0` | — | Incluir el mapa del repositorio con un presupuesto de N tokens (habilita repo-map solo cuando `> 0`). |

Sin `--format`/`--pack`, imprime hasta 5 sesiones recientes y hasta 20
observaciones recientes.

**Códigos de salida:** `0` éxito (incluyendo memorias previas ausentes); `1` en
error de render o de store. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex context
cortex context cortex --scope project
cortex context cortex --pack --budget=4096
```

**Ver también:** [search](#36-search).

### 3.9 `stats`

**Sinopsis**

```text
cortex stats
```

Imprime totales de sesiones, observaciones y proyectos. Los argumentos se ignoran.

**Flags:** ninguno. **Códigos de salida:** `0` éxito, `1` error de store.
**Autenticación:** ninguna.

**Ejemplo**

```bash
cortex stats
# Cortex Memory Stats
#   Sessions:     4
#   Observations: 128
#   Projects:     cortex
```

### 3.10 `timeline`

**Sinopsis**

```text
cortex timeline <observation_id> [--before N] [--after N]
```

Imprime observaciones adyacentes en el tiempo alrededor del target, con el target
resaltado.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `<observation_id>` | sí | ID de observación entero. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--before N` | `3` | — | Vecinos más antiguos a mostrar. Debe ser un entero. |
| `--after N` | `3` | — | Vecinos más nuevos a mostrar. Debe ser un entero. |

**Códigos de salida:** `0` éxito; `1` por ID ausente/inválido, conteos inválidos, o
error de store. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex timeline 42
cortex timeline 42 --before 5 --after 10
```

**Ver también:** [revisions](#311-revisions).

### 3.11 `revisions`

**Sinopsis**

```text
cortex revisions <observation_id> [--limit N]
```

Imprime el historial de revisiones temporal de una observación.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `<observation_id>` | sí | ID de observación entero. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--limit N` | `20` | — | Revisiones máximas a imprimir. Los valores no positivos caen a `20`. |

**Códigos de salida:** `0` éxito (incluyendo `No revision history found`); `1` por
ID ausente/inválido o error de store. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex revisions 42
cortex revisions 42 --limit 100
```

### 3.12 `setup`

**Sinopsis**

```text
cortex setup [agent] [flags]
cortex setup ollama [--base-url=URL] [--model=NAME]
```

Sin argumento de agente, imprime los agentes detectados y comandos rápidos.

**Argumentos**

| Argumento | Requerido | Descripción |
|---|---|---|
| `[agent]` | no | Uno de `opencode`, `claude-code`, `gemini-cli`, `codex`, `ollama`. |

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--profile[=]P` / `-p P` | `mcp.profile` (default `agent`) | `CORTEX_MCP_PROFILE` | Perfil de instalación: `dev` (11 tools), `minimal` (5 tools), `agent` (22 tools). |
| `--base-url=URL` | `http://localhost:11434` | — | Solo `setup ollama`: URL base de Ollama. |
| `--model=NAME` | `nomic-embed-text` | — | Solo `setup ollama`: modelo de embeddings a configurar. |

**Códigos de salida:** `0` éxito (incluyendo modo solo-detección); `1` en fallo de
instalación o de configuración de Ollama. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex setup
cortex setup claude-code --profile=dev
cortex setup ollama --model=nomic-embed-text
```

**Ver también:** [status](#327-status-mode), [MCP.md](MCP.md).

### 3.13 `import`

**Sinopsis**

```text
cortex import --from-json --path FILE
```

Importa observaciones desde un fichero de array JSON. La entrada está limitada a
**50 MiB** (`50 << 20`); el batch entero se valida de privacidad antes de cualquier
escritura.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--from-json` | *(token requerido primero)* | — | Selecciona la fuente de import JSON. |
| `--path FILE` | *(requerido)* | — | Ruta del fichero JSON de entrada. |

Los registros cuyo guardado falla se saltean con un warning; el comando aun así sale
con `0` y reporta `Imported <saved> of <total> observations from JSON`.

**Códigos de salida:** `0` éxito; `1` por fuente/`--path` ausente, fichero no
legible, JSON inválido, rechazo de privacidad, o error de app-open.
**Autenticación:** ninguna.

**Ejemplos**

```bash
cortex export --output backup.json
cortex import --from-json --path backup.json
```

**Ver también:** [export](#314-export), preflight de privacidad
[§2.3](#23-preflight-de-privacidad).

### 3.14 `export`

**Sinopsis**

```text
cortex export [--project P] [--output FILE]
cortex export --to-obsidian --vault PATH [flags]
```

Exporta observaciones a JSON (stdout o un fichero) o a una proyección de solo
lectura de Obsidian.

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--project P` | *(vacío)* | — | Filtrar por proyecto. También acepta `--project=P`. |
| `--output FILE` | *(stdout)* | — | Escribir JSON en `FILE` (modo `0600`). También acepta `--output=FILE`. |
| `--to-obsidian` | off | — | Cambiar a la proyección Obsidian. |
| `--vault PATH` | *(requerido con `--to-obsidian`)* | — | Directorio del vault destino. |
| `--include-personal` | off | — | Incluir observaciones de scope personal en la proyección. |

La exportación JSON lista hasta 10,000 observaciones.

**Códigos de salida:** `0` éxito; `1` por errores de store/escritura/marshal, o
`--to-obsidian` sin `--vault`. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex export --project cortex --output cortex.json
cortex export --to-obsidian --vault ~/vault/synapse --project cortex
```

**Ver también:** [import](#313-import).

### 3.15 `sync`

**Sinopsis**

```text
cortex sync [--import | --status | --all] [--project P]
cortex sync --remote
```

Sincroniza observaciones a través de chunks de fichero (default: export) o
replicación remota.

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--import` | off | — | Importar chunks pendientes en lugar de exportar. |
| `--status` | off | — | Imprimir conteos de chunks local/remoto e importaciones pendientes. |
| `--all` | off | — | Exportar todos los proyectos (sin auto-detección de proyecto). |
| `--project P` | *(auto-detectado)* | — | Restringir a un proyecto. |
| `--remote` | off | — | Replicar contra el endpoint remoto configurado (`sync.url`). |

Por defecto (sin `--import`/`--status`/`--remote`): exporta un chunk para el
proyecto detectado. `--remote` requiere `sync.url`; el bearer se resuelve desde
`sync.token_env` (default `CORTEX_REMOTE_TOKEN`), con fallback a `http.token`.

**Códigos de salida:** `0` éxito; `1` por URL remota ausente, errores de sync, o
fallo de app-open. **Autenticación:** la replicación remota requiere un bearer; el
transporte por fichero no requiere ninguno.

**Ejemplos**

```bash
cortex sync --status
cortex sync --all
cortex sync --remote
```

**Ver también:** [CONFIGURATION.md](CONFIGURATION.md).

### 3.16 `merge-projects`

**Sinopsis**

```text
cortex merge-projects --from "Name1,Name2" --to canonical-name [--dry-run]
```

Fusiona variantes de nombre de proyecto en un único nombre canónico.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--from LIST` | *(requerido)* | — | Nombres fuente separados por comas. |
| `--to NAME` | *(requerido)* | — | Nombre canónico destino. |
| `--dry-run` | off | — | Imprimir la fusión planificada sin escribir. |

**Códigos de salida:** `0` éxito (incluyendo dry run); `1` por `--from`/`--to`
ausente o error de fusión. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex merge-projects --from "cortex,cortex-ia" --to cortex --dry-run
cortex merge-projects --from "cortex,cortex-ia" --to cortex
```

### 3.17 `reindex`

**Sinopsis**

```text
cortex reindex [--project P]
```

Regenera los embeddings vectoriales de las observaciones usando el proveedor de
embeddings configurado. Requiere un proveedor de embeddings configurado y un vector
store healthy (build con `-tags cortex_vectors` para el adaptador SQLite BLOB).
Procesa hasta **5000** observaciones por run e imprime progreso cada 50.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--project P` | *(todos los proyectos)* | — | Restringir el reindexado a un proyecto. |

Cuando el proveedor es `ollama` y `search.ollama_auto_start` es true, el comando
arranca Ollama y hace pull del modelo si falta.

**Códigos de salida:** `0` éxito (incluyendo cero observaciones); `1` cuando no hay
proveedor de embeddings configurado, el vector store no está disponible, o ocurre un
error. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex reindex
cortex reindex --project cortex
```

**Ver también:** [`code scan`](#323-code), [CONFIGURATION.md](CONFIGURATION.md).

### 3.18 `doctor`

**Sinopsis**

```text
cortex doctor
cortex doctor --server [URL]
```

Ejecuta health checks locales, o sondea un servidor en marcha cuando se da
`--server`.

**Flags**

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--server [URL]` | off; URL default `http://localhost:7438` | `CORTEX_HTTP_TOKEN` (para la sonda bearer) | Sondear un servidor en lugar del store local. Si el siguiente token no empieza por `-`, se usa como URL base. |

Comprobaciones locales: modo operativo, base de datos, búsqueda FTS5, grafo de
conocimiento, vector store, embeddings, ratio de huérfanos, índice de código AST,
daemon de Ollama, y un aviso de aislamiento de claves de vendor. Cualquier
comprobación que falle cuenta como issue y el comando sale con `1`.

Comprobaciones de servidor: `/health` (HTTP 200), verificación bearer opcional
contra `/mcp` cuando `CORTEX_HTTP_TOKEN` (o `CORTEX_REMOTE_TOKEN`) está fijado, y
una sonda de la web UI embebida. Un fallo de sonda del servidor es informativo y no
cambia la semántica de códigos de salida de la línea informativa.

**Códigos de salida:** `0` todas las comprobaciones pasaron; `1` uno o más issues.
**Autenticación:** ninguna localmente; la sonda del servidor usa
`CORTEX_HTTP_TOKEN`.

**Ejemplos**

```bash
cortex doctor
cortex doctor --server
cortex doctor --server http://localhost:7438
```

**Ver también:** [`status`](#327-status-mode).

### 3.19 `gc`

**Sinopsis**

```text
cortex gc [--days N]
```

Borra (hard-delete) observaciones soft-deleted/archivadas de más de `N` días (hasta
1000 candidatos por run).

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--days N` | `90` | — | Corte de antigüedad en días. Solo se aceptan enteros positivos; los valores inválidos se ignoran. |

**Códigos de salida:** `0` éxito (incluyendo `Nothing to collect.`); `1` en error
de listado o borrado. **Autenticación:** ninguna. **Warning:** esta es una
operación destructiva.

**Ejemplos**

```bash
cortex gc
cortex gc --days 30
```

### 3.20 `backup`

**Sinopsis**

```text
cortex backup [path]
```

Crea un snapshot atómico en línea de la base de datos SQLite usando
`VACUUM INTO`.

| Argumento | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `[path]` | `cortex-backup-<YYYYMMDD-HHMMSS>.db` | — | Ruta de destino. Un destino existente se elimina primero. |

**Códigos de salida:** `0` éxito; `1` en resolución de ruta, app-open, o error de
backup. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex backup
cortex backup ~/backups/cortex.db
```

**Ver también:** [migrate](#328-migrate).

### 3.21 `watch`

**Sinopsis**

```text
cortex watch [path] [--project NAME]
```

Observa un repositorio y re-indexa incrementalmente símbolos AST en
creación/cambio/borrado de ficheros hasta ser interrumpido (Ctrl+C). Imprime
`👀 Cortex File Watcher activo en …` al inicio y `🛑 File Watcher detenido.` al
parar (las cadenas de progreso en vivo del watcher están actualmente en español;
esta es una peculiaridad cosmética conocida).

| Argumento / Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `[path]` | `.` | — | Directorio a observar (resuelto a una ruta absoluta). |
| `--project NAME` / `--project=NAME` | auto-detectado, si no `default` | — | Nombre del proyecto para los símbolos indexados. |

**Códigos de salida:** `0` parada limpia; `1` en resolución de directorio, app-open,
o error del watcher. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex watch
cortex watch ./internal --project cortex
```

**Ver también:** [ingest](#322-ingest), [`code scan`](#323-code).

### 3.22 `ingest`

**Sinopsis**

```text
cortex ingest [path] [--project NAME] [--max-files N]
```

Escanear un codebase una vez con el extractor AST estático zero-CGO, persistir
símbolos y relaciones, e imprimir analíticas Graphify (ficheros, cohesión, god
nodes, ciclos de import).

**Argumentos / Flags**

| Argumento / Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `[path]` | `.` | — | Directorio a escanear. |
| `--project NAME` / `-p NAME` | auto-detectado, si no `default` | — | Nombre del proyecto. |
| `--max-files N` / `-m N` | `500` | — | Ficheros máximos a escanear. Solo se aceptan enteros positivos. |

**Códigos de salida:** `0` éxito; `1` en extracción, app-open, o error de guardado.
**Autenticación:** ninguna.

**Ejemplos**

```bash
cortex ingest
cortex ingest ./internal --project cortex --max-files 200
```

**Ver también:** [`code scan`](#323-code), [watch](#321-watch).

### 3.23 `code`

**Sinopsis**

```text
cortex code <subcommand> [arguments] [flags]
```

Inteligencia AST de código. `cortex code`, `cortex code help`,
`cortex code --help` y `cortex code -h` imprimen la lista de subcomandos y salen
con `0`.

Subcomandos:

| Subcomando | Propósito |
|---|---|
| [`scan`](#code-scan) | Escanear un repositorio e indexar símbolos AST (igual que `ingest`). |
| [`symbols`](#code-symbols) | Listar símbolos indexados. |
| [`analyze`](#code-analyze) | Ejecutar analíticas arquitectónicas Graphify. |
| [`impact`](#code-impact) | Calcular el blast radius de un símbolo o fichero. |
| [`diff`](#code-diff) | Blast radius de cambios Git sin commitear. |
| [`graph`](#code-graph) | Exportar/visualizar el grafo de dependencias. |
| [`map`](#code-map) | Generar un repo map con presupuesto de tokens. |
| [`tests`](#code-tests) | Hallar ficheros/funciones de test impactados. |
| [`find`](#code-find) | Buscar símbolos por substring o regex. |

Todos los subcomandos usan `default` para `--project` salvo indicación.

#### `code scan`

```text
cortex code scan [path] [--project=NAME] [--max-files=N]
```

Delega en [`ingest`](#322-ingest); flags y defaults idénticos
(`--max-files` default `500`).

#### `code symbols`

```text
cortex code symbols [--project=NAME] [--kind=KIND] [--file=PATH]
```

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--kind=KIND` | *(vacío)* | — | Filtrar por kind de símbolo. |
| `--file=PATH` | *(vacío)* | — | Filtrar por ruta de fichero. |

Lista hasta 100 símbolos.

#### `code analyze`

```text
cortex code analyze [--project=NAME]
```

Imprime totales, cohesión media, god nodes y ciclos de import.

#### `code impact`

```text
cortex code impact <target> [--project=NAME] [--hops=N] [--json]
```

| Flags/Args | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `<target>` | *(requerido)* | — | Nombre de símbolo, ID o ruta de fichero. |
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--hops=N` | `3` | — | Profundidad de traversión. |
| `--json` | off | — | Emitir JSON. |

`blast-radius` es un alias de `impact`.

#### `code diff`

```text
cortex code diff [--staged] [--project=NAME] [--hops=N] [--json]
```

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--staged` / `--cached` | off | — | Analizar el área de staging de Git en lugar del working tree. |
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--hops=N` | `3` | — | Profundidad de traversión. |
| `--json` | off | — | Emitir JSON. |

Requiere `git` y un repositorio Git.

#### `code graph`

```text
cortex code graph [--project=NAME] [--format=mermaid] [--symbol=S] [--hops=N] [--max-nodes=N]
```

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--format=F` | `mermaid` | — | Formato de salida: `mermaid`, `ascii` (alias `tree`), `json`. Formatos desconocidos salen con `1`. |
| `--symbol=S` | *(vacío)* | — | Símbolo raíz sobre el que centrar el grafo. |
| `--hops=N` | `2` | — | Profundidad de traversión cuando se da un símbolo raíz. |
| `--max-nodes=N` | `50` | — | Nodos máximos. |

#### `code map`

```text
cortex code map [--project=NAME] [--budget=N]
```

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--budget=N` | `2048` | — | Presupuesto de tokens. |

`repo-map` es un alias de `map`.

#### `code tests`

```text
cortex code tests <target> [--project=NAME] [--hops=N] [--json]
```

| Flags/Args | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `<target>` | *(requerido)* | — | Nombre de símbolo, ID o ruta de fichero. |
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--hops=N` | `3` | — | Profundidad de traversión. |
| `--json` | off | — | Emitir JSON. |

`test-map` e `impacted-tests` son alias de `tests`.

#### `code find`

```text
cortex code find <query> [--project=NAME] [--kind=K] [--file=PATH] [--limit=N] [--regex] [--json]
```

| Flags/Args | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `<query>` | *(requerido)* | — | Query por substring o regex. |
| `--project=NAME` | `default` | — | Nombre del proyecto. |
| `--kind=K` | *(vacío)* | — | Filtrar por kind de símbolo. |
| `--file=PATH` | *(vacío)* | — | Filtrar por ruta de fichero. |
| `--limit=N` | `50` | — | Resultados máximos. |
| `--regex` | off | — | Tratar la query como expresión regular. |
| `--json` | off | — | Emitir JSON. |

`search-symbols` es un alias de `find`.

**Códigos de salida:** `0` éxito; `1` por targets/queries requeridos ausentes,
formatos desconocidos, subcomandos desconocidos, o errores de store.
**Autenticación:** ninguna.

**Ejemplos**

```bash
cortex code scan . --project cortex
cortex code symbols --project cortex --kind func
cortex code impact CalculateBlastRadius --project cortex --hops=2
cortex code diff --staged --json
cortex code graph --project cortex --format=ascii --max-nodes=30
cortex code tests CalculateBlastRadius
cortex code find "RepoMap" --project cortex --regex
```

**Ver también:** [ingest](#322-ingest), [watch](#321-watch).

### 3.24 `config`

**Sinopsis**

```text
cortex config <subcommand> [arguments] [flags]
```

Gestiona la configuración sin editar ficheros a mano.

| Subcomando | Propósito |
|---|---|
| `get <key> [--file=PATH]` | Imprimir un valor. |
| `set <key> <value> [--file=PATH] [--format=FMT]` | Fijar un valor y persistirlo. |
| `show` / `print` | Imprimir la config activa (`--format` `yaml` por defecto o `json`; secretos enmascarados). |
| `validate` | Validar un fichero de configuración. |
| `init` | Escribir un fichero de configuración mínimo. |
| `path` | Imprimir la ruta resuelta del fichero de configuración. |
| `wizard` / `interactive` | Lanzar el asistente de configuración. |

| Flag | Aplica a | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|---|
| `--file PATH` / `-f` / `--config` | `get`, `set`, `show`, `validate` | *(descubierto)* | — | Fichero de config explícito. También acepta `--file=PATH` / `--config=PATH`. |
| `--format FMT` | `show`, `init`, `set` | `yaml` | — | `yaml` o `json` para `show`; extensión `yaml`/`json`/`toml` para `set`/`init`. |
| `--force` / `-f` | `init` | off | — | Sobreescribir un fichero existente. |
| `-o PATH` / `--output PATH` | `init` | *(dir de config default)* | — | Ruta de salida. |
| `--cli` | `wizard` | off | — | Forzar el asistente CLI. |
| `--tui` | `wizard` | off | — | Forzar el asistente TUI. |

Cuando `wizard` se ejecuta sin ninguno de los dos flags, lanza el asistente TUI si
stdout es una terminal; si no, cae al resumen CLI.

Claves configurables (vía `get`/`set`): `ai.provider` (alias `llm.provider`),
`ai.model`, `ai.base_url`, `database.path`, `database.in_memory`, `http.enabled`,
`http.port`, `http.host`, `http.token`, `logging.level`, `logging.format`,
`search.embedding_provider` (alias `embedding.provider`),
`search.embedding_model`, `search.embedding_base_url`, `search.vector`,
`search.fts5`, `search.ollama_auto_start`, `mcp.enabled`, `mcp.profile`,
`mcp.remote.enabled`, `mcp.remote.url`, `mcp.remote.token_env`, `sync.enabled`,
`sync.url`, `sync.token_env`, `sync.interval`, `memory.auto_archive_days`,
`memory.importance_decay_half_life`, `memory.min_archive_score`, `vector.provider`.

`config show` enmascara `http.token`, `vector.qdrant.api_key`,
`server.secrets.signing_key` y `server.secrets.oidc_client_secret`.

**Códigos de salida:** `0` éxito; `1` por subcomando o clave ausente/desconocida,
valor inválido, o error de load/save. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex config path
cortex config get http.port
cortex config set http.port 8080
cortex config set mcp.profile minimal
cortex config show --format=json
cortex config validate --file ./cortex.yaml
cortex config init --format=toml --force
cortex config wizard --cli
```

**Ver también:** [CONFIGURATION.md](CONFIGURATION.md).

### 3.25 `auth`

**Sinopsis**

```text
cortex auth <login|status|logout> [options]
```

Gestiona el token de autenticación local almacenado en `http.token`.

| Subcomando | Flags | Comportamiento |
|---|---|---|
| `login` | `--token TOKEN` / `-t` / `--token=TOKEN` | Persiste el token en la config cargada. Requiere un token no vacío. |
| `status` / `whoami` | ninguno | Imprime modo, token enmascarado, principal y rol. |
| `logout` | ninguno | Limpia `http.token` y guarda. |

**Códigos de salida:** `0` éxito; `1` por subcomando ausente, token de login
ausente, o error de load/save. **Autenticación:** ninguna (este comando gestiona la
credencial en sí).

**Ejemplos**

```bash
cortex auth login --token=<bearer>
cortex auth status
cortex auth logout
```

**Ver también:** [§5 Matriz de autenticación](#5-matriz-de-autenticacion).

### 3.26 `web`

**Sinopsis**

```text
cortex web key show [--key-file PATH]
cortex web key regenerate [--key-file PATH]
```

Gestiona la clave de acceso web embebida (independiente de `http.token`).

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--key-file PATH` / `-k` / `--key-file=PATH` | `<config dir>/web.key` | — | Ubicación del fichero de clave. |

- `show` reporta si existe una clave, su ruta de fichero y su prefijo. Nunca
  imprime el plaintext.
- `regenerate` rota la clave atómicamente e imprime el nuevo plaintext exactamente
  una vez; la clave anterior queda invalidada.

**Códigos de salida:** `0` éxito; `1` por subcomando ausente, argumento inesperado,
o error del store de claves. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex web key show
cortex web key regenerate
```

**Ver también:** [serve](#34-serve), [`--mode server`](#17-mode-server).

### 3.27 `status` / `mode`

**Sinopsis**

```text
cortex status [--json]
cortex mode [--json]
```

Imprime el modo operativo, la ruta de la base de datos, el estado de sync, los
embeddings y la ruta de config. `mode` es un alias de `status`.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--json` | off | — | Emitir un objeto JSON en lugar del bloque legible por humanos. |

La forma del JSON es:

```json
{
  "mode": "local",
  "description": "Local (Zero-CGO SQLite Standalone Memory)",
  "database": { "path": "…", "in_memory": false },
  "sync": { "enabled": false, "url": "" },
  "embeddings": "disabled / not configured",
  "config_path": "…"
}
```

El modo efectivo se resuelve desde `CORTEX_MODE`, después `server.storage.dsn`,
después `sync.enabled` + `sync.url`, y finalmente `local`.

**Códigos de salida:** `0` éxito. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex status
cortex status --json
cortex mode --json
```

**Ver también:** [doctor](#318-doctor), [§1.2 tríada de modos](#12-la-triada-de-modos).

### 3.28 `migrate`

**Sinopsis**

```text
cortex migrate <up|down|status> [--target VERSION]
```

Gestiona las migraciones de base de datos.

| Subcomando | Comportamiento |
|---|---|
| `up` | Aplicar todas las migraciones pendientes. |
| `down [--target N]` | Retroceder a la versión `N` (default `0`). |
| `status` | Imprimir la versión, nombre y timestamp de aplicación de cada migración. |

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--target N` | `0` | — | Versión destino para `down`. |

La línea base local v2 es forward-only. `migrate down` no es una operación normal
soportada y las bases de datos v2 existentes no deben ser degradadas automáticamente.

**Códigos de salida:** `0` éxito; `1` por subcomando ausente/desconocido, target
inválido, o error de migración. **Autenticación:** ninguna.

**Ejemplos**

```bash
cortex migrate status
cortex migrate up
```

**Ver también:** [backup](#320-backup).

### 3.29 `update`

**Sinopsis**

```text
cortex update [--check]
```

Actualiza Cortex al último release, o comprueba si hay uno.

| Flag | Default | Sobrescritura por entorno | Descripción |
|---|---|---|---|
| `--check` / `-c` | off | — | Reportar el último release sin instalarlo. |

Cuando existe un release más nuevo, `--check` imprime la versión, las release notes
y la página de release. Sin `--check`, se intenta un self-update.

**Códigos de salida:** `0` éxito (incluyendo up-to-date); `1` cuando la
actualización falla. **Autenticación:** ninguna (usa el canal público de releases).

**Ejemplos**

```bash
cortex update --check
cortex update
```

---

## 4. Índice de variables de entorno

Cortex resuelve la configuración primero desde el fichero de configuración y
después desde variables de entorno. Cada clave de configuración es automáticamente
direccionable como `CORTEX_` + la clave en mayúsculas con `.` reemplazado por `_`
(viper `AutomaticEnv` con el prefijo `CORTEX`). Por ejemplo `http.port` se convierte
en `CORTEX_HTTP_PORT`. Las variables de abajo se listan en orden alfabético; la
columna "Default" es el default efectivo cuando no se fija ni el fichero de
configuración ni la variable.

### 4.1 Core e identidad

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_MODE` | — | *(vacío → `local`)* | Selecciona la tríada de modos: `local`, `hybrid`, `server`. |
| `CORTEX_SERVER_NAME` | `server.name` | `cortex` | Nombre del servidor. |
| `CORTEX_SERVER_VERSION` | `server.version` | `2.0.0` | Etiqueta de versión del servidor. |
| `CORTEX_SERVER_TENANT_ID` | `server.tenant_id` | *(vacío)* | Tenant UUID atado al principal sintético. Requerido en modo servidor. |
| `CORTEX_SERVER_WORKSPACE_ID` | `server.workspace_id` | *(vacío)* | Workspace UUID por defecto. Requerido en modo servidor. |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | `server.principal_subject` | *(vacío)* | Subject UUID del principal sintético constante. Requerido en modo servidor. |
| `CORTEX_SERVER_ROLES` | `server.roles` | `[]` | Roles separados por comas para el principal configurado. |
| `CORTEX_SERVER_SCOPES` | `server.scopes` | `[]` | Scopes autorizados separados por comas. |
| `CORTEX_SERVER_PROJECT_IDS` | `server.project_ids` | `[]` | IDs de proyecto separados por comas. |
| `CORTEX_SERVER_CLASSIFICATION_CLEARANCE` | `server.classification_clearance` | `[]` | Labels de classification clearance separados por comas. |
| `CORTEX_SERVER_GRANT_DIGEST` | `server.grant_digest` | `""` | Obsoleto; calculado dinámicamente por PostgreSQL. |
| `CORTEX_SERVER_GRANT_VERSION` | `server.grant_version` | `0` | Obsoleto; aprovisionado dinámicamente por PostgreSQL. |
| `CORTEX_SERVER_SECRETS_SIGNING_KEY` | `server.secrets.signing_key` | `""` | Secreto de firmado del servidor. Enmascarado por `config show`. |
| `CORTEX_SERVER_SECRETS_OIDC_CLIENT_SECRET` | `server.secrets.oidc_client_secret` | `""` | Client secret OIDC. Enmascarado por `config show`. |

### 4.2 Almacenamiento y arranque del servidor

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_SERVER_STORAGE_DRIVER` | `server.storage.driver` | `postgres` | Driver de persistencia (`postgres` servidor / `sqlite` local). |
| `CORTEX_SERVER_STORAGE_DSN` | `server.storage.dsn` | *(vacío)* | DSN de PostgreSQL de runtime no privilegiado. |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | `server.storage.migration_dsn` | *(vacío)* | DSN de migración privilegiado. Los roles de runtime y migración deben diferir. |
| `CORTEX_SERVER_STORAGE_MAX_CONNS` | `server.storage.max_conns` | `10` | Conexiones máximas del pool. |
| `CORTEX_SERVER_PROVIDER_EMBEDDING` | `server.provider.embedding` | `none` | Selección de proveedor de embeddings del servidor. |
| `CORTEX_SERVER_PROVIDER_VECTOR` | `server.provider.vector` | `none` | Selección de proveedor vectorial del servidor. |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | `server.railway_internal_embedding_host` | `""` | Hostname privado de Railway permitido para embeddings por HTTP. |
| `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | `server.bootstrap_development` | `false` (opt-in; inseguro en producción) | Modo solo-desarrollo que reutiliza el DSN de runtime para las migraciones cuando no se separa un rol de migración dedicado. Los ficheros compose lo incluyen como source por defecto para los contenedores; nunca lo actives en un despliegue compartido. |
| `CORTEX_SERVER_AUTO_BOOTSTRAP` | *(solo compose/entrypoint)* | `true` en compose | Habilita la vía de bootstrap automático del entrypoint del contenedor. |
| `CORTEX_SERVER_PRINT_BOOTSTRAP` | *(solo entrypoint)* | `false` (opt-in; inseguro en producción) | Controla el eco del bearer de bootstrap en los logs del contenedor. Déjalo sin fijar en producción; de otro modo el bearer solo se escribe en el fichero de estado. |
| `CORTEX_BOOTSTRAP_STATE_FILE` | *(solo entrypoint)* | `/home/cortex/.cortex/server-bootstrap.env` | Ubicación del estado de bootstrap persistido en el contenedor. |
| `CORTEX_SERVER_MULTI_TENANT` | *(clave retirada)* | `false` | Clave de configuración eliminada. Cargarla se tolera pero se ignora y se descarta en el siguiente guardado. |

### 4.3 HTTP, logging y MCP

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_HTTP_ENABLED` | `http.enabled` | `true` | Habilita `/api/*` y `/mcp`. |
| `CORTEX_HTTP_HOST` | `http.host` | `localhost` | Interfaz de escucha (`0.0.0.0` para contenedores). |
| `CORTEX_HTTP_PORT` | `http.port` | `7438` | Puerto de escucha. `CORTEX_PORT` intencionalmente no se respeta. |
| `CORTEX_HTTP_TOKEN` | `http.token` | `""` | Bearer estático para el HTTP/MCP local y el plano de peticiones del servidor. Requerido para binds no-loopback. |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | `http.allowed_origins` | `[]` | Orígenes CORS separados por comas. |
| `CORTEX_LOGGING_LEVEL` | `logging.level` | `info` | `debug`, `info`, `warn`, `error`. |
| `CORTEX_LOGGING_FORMAT` | `logging.format` | `json` | `json`, `text`, `plain`. |
| `CORTEX_MCP_ENABLED` | `mcp.enabled` | `true` | Habilita el MCP local. |
| `CORTEX_MCP_PROFILE` | `mcp.profile` | `agent` | `agent`, `dev`, `minimal`. |
| `CORTEX_MCP_REMOTE_ENABLED` | `mcp.remote.enabled` | `false` | Hacer proxy de un servidor MCP remoto. |
| `CORTEX_MCP_REMOTE_URL` | `mcp.remote.url` | `""` | Destino MCP remoto. |
| `CORTEX_MCP_REMOTE_TOKEN_ENV` | `mcp.remote.token_env` | `CORTEX_REMOTE_TOKEN` | Nombre de la variable que contiene el bearer remoto. |
| `CORTEX_MCP_REMOTE_TIMEOUT` | `mcp.remote.timeout` | `30s` | Timeout del MCP remoto. |
| `CORTEX_REMOTE_TOKEN` | *(target de token env)* | *(vacío)* | Contenedor por defecto para los tokens bearer de MCP/sync remotos. |

### 4.4 Base de datos y almacenamiento

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_DATABASE_PATH` | `database.path` | `~/.cortex/cortex.db` | Ruta de la base de datos SQLite. |
| `CORTEX_DATABASE_IN_MEMORY` | `database.in_memory` | `false` | Usar una base de datos en memoria. |
| `CORTEX_DATABASE_PRAGMA_JOURNAL_MODE` | `database.pragma.journal_mode` | `WAL` | Modo de journal de SQLite. |
| `CORTEX_DATABASE_PRAGMA_SYNCHRONOUS` | `database.pragma.synchronous` | `NORMAL` | Modo synchronous de SQLite. |
| `CORTEX_DATABASE_PRAGMA_CACHE_SIZE` | `database.pragma.cache_size` | `-64000` | Tamaño de caché de SQLite (KiB). |
| `CORTEX_DATABASE_PRAGMA_FOREIGN_KEYS` | `database.pragma.foreign_keys` | `true` | Hacer cumplir las foreign keys. |
| `CORTEX_DATABASE_PRAGMA_TEMP_STORE` | `database.pragma.temp_store` | `MEMORY` | Ubicación del store temporal. |
| `CORTEX_DATABASE_PRAGMA_MMAP_SIZE` | `database.pragma.mmap_size` | `268435456` | Tamaño de mmap. |

### 4.5 Búsqueda, embeddings y LLM

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_SEARCH_DEFAULT_LIMIT` | `search.default_limit` | `20` | Límite de resultados por defecto para queries al store. |
| `CORTEX_SEARCH_MAX_LIMIT` | `search.max_limit` | `100` | Límite máximo de resultados. |
| `CORTEX_SEARCH_FTS5` | `search.fts5` | `true` | Habilitar FTS5. |
| `CORTEX_SEARCH_VECTOR` | `search.vector` | `false` | Habilitar retrieval vectorial denso. |
| `CORTEX_SEARCH_FUSION_K` | `search.fusion_k` | `60` | Constante de fusión RRF. |
| `CORTEX_SEARCH_OLLAMA_AUTO_START` | `search.ollama_auto_start` | `false` | Auto-arrancar Ollama cuando es el proveedor de embeddings. |
| `CORTEX_SEARCH_EMBEDDING_PROVIDER` | `search.embedding_provider` | *(vacío)* | Proveedor de embeddings (alias auto-mapeado). |
| `CORTEX_SEARCH_EMBEDDING_MODEL` | `search.embedding_model` | *(vacío)* | Modelo de embeddings (alias auto-mapeado). |
| `CORTEX_SEARCH_EMBEDDING_BASE_URL` | `search.embedding_base_url` | *(vacío)* | URL base de embeddings (alias auto-mapeado). |
| `CORTEX_EMBEDDING_PROVIDER` | `search.embedding_provider` | `none` | Proveedor de embeddings: `none`, `openai`, `ollama`, `openai-compatible`. Los valores desconocidos fallan en modo cerrado al cargar. |
| `CORTEX_EMBEDDING_MODEL` | `search.embedding_model` | `text-embedding-3-small` | Identificador del modelo de embeddings. |
| `CORTEX_EMBEDDING_BASE_URL` | `search.embedding_base_url` | `http://localhost:11434` (Ollama) | URL base de embeddings. |
| `CORTEX_EMBEDDING_API_KEY` | *(credencial)* | *(vacío)* | Credencial de embeddings. Requerida para proveedores de embeddings alojados. |
| `CORTEX_LLM_PROVIDER` | `ai.provider` | *(vacío)* | Proveedor LLM. |
| `CORTEX_LLM_MODEL` | `ai.model` | *(vacío)* | Modelo LLM. |
| `CORTEX_LLM_BASE_URL` | `ai.base_url` | *(vacío)* | URL base del LLM. |
| `CORTEX_LLM_API_KEY` | *(credencial)* | *(vacío)* | Credencial LLM. Variables de vendor como `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` se ignoran intencionadamente. |
| `CORTEX_LLM_ALLOWED_HOSTS` | *(server LLM)* | *(vacío)* | Hosts LLM permitidos separados por comas. |
| `CORTEX_LLM_ALLOWED_PORTS` | *(server LLM)* | *(vacío)* | Puertos LLM permitidos separados por comas. |
| `CORTEX_LLM_ALLOW_LOOPBACK` | *(server LLM)* | `false` | Permitir destinos LLM de loopback. |
| `CORTEX_LLM_ALLOW_LOOPBACK_HTTP` | *(server LLM)* | `false` | Permitir destinos de loopback HTTP plano. |
| `CORTEX_LLM_MAX_CONCURRENT` | *(server LLM)* | *(default del proveedor)* | Peticiones LLM concurrentes máximas. |
| `CORTEX_LLM_MAX_REDIRECTS` | *(server LLM)* | *(default del proveedor)* | Redirects máximos. |
| `CORTEX_LLM_MAX_RESPONSE_BODY_BYTES` | *(server LLM)* | *(default del proveedor)* | Tamaño máximo del body de respuesta. |
| `CORTEX_LLM_MAX_ERROR_BODY_BYTES` | *(server LLM)* | *(default del proveedor)* | Tamaño máximo del body de error. |
| `CORTEX_LLM_TIMEOUT` | *(server LLM)* | *(default del proveedor)* | Timeout de petición LLM (duración). |
| `CORTEX_LLM_CA_FILE` | *(server LLM)* | *(vacío)* | Bundle CA PEM para el handshake TLS del LLM. |

### 4.6 Memoria, lifecycle y vectores

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_MEMORY_MAX_OBSERVATION_LENGTH` | `memory.max_observation_length` | `50000` | Longitud máxima de observación. |
| `CORTEX_MEMORY_DEDUPE_WINDOW` | `memory.dedupe_window` | `15m` | Ventana de dedupe. |
| `CORTEX_MEMORY_AUTO_ARCHIVE_DAYS` | `memory.auto_archive_days` | `90` | Antigüedad de auto-archivo. |
| `CORTEX_MEMORY_IMPORTANCE_DECAY_HALF_LIFE` | `memory.importance_decay_half_life` | `30` | Vida media del decay de importancia (días). |
| `CORTEX_MEMORY_MIN_ARCHIVE_SCORE` | `memory.min_archive_score` | `0.1` | Score mínimo a retener. |
| `CORTEX_LIFECYCLE_ENABLE_AUTO_ARCHIVE` | `lifecycle.enable_auto_archive` | `true` | Habilitar el bucle de auto-archivo. |
| `CORTEX_LIFECYCLE_ARCHIVE_CHECK_INTERVAL` | `lifecycle.archive_check_interval` | `1h` | Intervalo de comprobación de archivo. |
| `CORTEX_VECTOR_PROVIDER` | `vector.provider` | *(vacío → `sqlite_blob`)* | Backend vectorial: sin fijar (BLOB local), `qdrant`, o `pgvector`. |
| `CORTEX_VECTOR_PGVECTOR_DIMENSION` | *(server pgvector)* | resuelto desde el modelo | Override de dimensión pgvector. |
| `CORTEX_VECTOR_PGVECTOR_MAX_PARALLEL_WORKERS_PER_GATHER` | *(tuning pgvector del servidor)* | *(sin fijar → desactivado)* | Activa `SET LOCAL max_parallel_workers_per_gather = N` dentro de las transacciones de búsqueda exact-scan pgvector. Un entero positivo activa el ajuste; sin fijar, no numérico o `<= 0` mantiene el valor por defecto (sin `SET LOCAL`). |
| `CORTEX_VECTOR_PGVECTOR_DISTANCE_MODE` | *(tuning pgvector del servidor)* | `cosine` | Operador de distancia exact-scan: `cosine` (`<=>`, por defecto) o `ip` (`<#>` producto interno normalizado, para corpus pre-normalizados). Los valores no reconocidos vuelven a `cosine`. |
| `CORTEX_VECTOR_QDRANT_HOST` | `vector.qdrant.host` | `localhost` | Host de Qdrant (auto-mapeado; el adaptador es solo-servidor). |
| `CORTEX_VECTOR_QDRANT_PORT` | `vector.qdrant.port` | `6334` | Puerto de Qdrant (auto-mapeado). |
| `CORTEX_VECTOR_QDRANT_COLLECTION` | `vector.qdrant.collection` | `cortex` | Colección de Qdrant (auto-mapeada). |
| `CORTEX_VECTOR_QDRANT_API_KEY` | `vector.qdrant.api_key` | `""` | API key de Qdrant (enmascarada por `config show`). |
| `CORTEX_VECTOR_PGVECTOR_SCHEMA` | `vector.pgvector.schema` | `cortex_vector` | Esquema pgvector. |
| `CORTEX_VECTOR_PGVECTOR_TABLE` | `vector.pgvector.table` | `embeddings` | Tabla pgvector. |
| `CORTEX_VECTOR_PGVECTOR_DSN` | `vector.pgvector.dsn` | *(vacío)* | DSN pgvector (solo-servidor). |

### 4.7 Sync

| Variable | Clave de config | Default | Significado |
|---|---|---|---|
| `CORTEX_SYNC_ENABLED` | `sync.enabled` | `false` | Habilitar la sincronización. |
| `CORTEX_SYNC_URL` | `sync.url` | `""` | Endpoint de sync remoto. |
| `CORTEX_SYNC_TOKEN_ENV` | `sync.token_env` | `CORTEX_REMOTE_TOKEN` | Nombre de la variable que contiene el bearer de sync. |
| `CORTEX_SYNC_INTERVAL` | `sync.interval` | `30s` | Intervalo de sync. |
| `CORTEX_SYNC_TIMEOUT` | `sync.timeout` | `30s` | Timeout de sync. |

### 4.8 Variables ignoradas, legacy y solo-test

| Variable | Estado | Significado |
|---|---|---|
| `CORTEX_AI_API_KEY` | **Ignorada** | No aceptada; usa `CORTEX_LLM_API_KEY` o `CORTEX_EMBEDDING_API_KEY`. |
| `CORTEX_SEARCH_EMBEDDING_API_KEY` | **Ignorada (legacy)** | No aceptada; usa `CORTEX_EMBEDDING_API_KEY`. |
| `CORTEX_TEST_POSTGRES_DSN` | Solo-test | DSN de runtime para tests de integración (falla cuando falta, nunca se salta). |
| `CORTEX_TEST_POSTGRES_MIGRATION_DSN` | Solo-test | DSN de migración para tests de integración. |
| `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN` | Solo-test | DSN authz-admin para tests de integración. |
| `CORTEX_QDRANT_HOST` / `CORTEX_QDRANT_PORT` | Solo-test | Target del test de integración del adaptador Qdrant. |
| `CORTEX_PGVECTOR_DSN` | Solo-test | Target del test de integración del adaptador pgvector. |
| `CORTEX_SPIKE_PG_ADMIN_DSN` / `CORTEX_SPIKE_PGBOUNCER_DSN` | Solo-test | DSNs de spike de gobernanza. |
| `CORTEX_RESOURCE_HELPER_MARKER` | Solo-test | Marker de helper de recursos de benchmarks. |

---

## 5. Matriz de autenticación

Cortex tiene cuatro planos de credenciales. La tabla indica qué credencial aplica
por comando y por transporte.

| Comando / transporte | SQLite local | HTTP local (`serve`) | Servidor (`--mode server`) | MCP |
|---|---|---|---|---|
| `search`, `save`, `context`, `stats`, `timeline`, `revisions`, `export`, `import`, `merge-projects`, `gc`, `backup`, `reindex`, `ingest`, `code`, `watch`, `tui`, `doctor` (local), `status`, `migrate`, `config`, `setup` | **Sin auth** (local zero-auth) | n/a | n/a | n/a |
| `serve` | — | `/health` sin autenticar; `/api/*` y el MCP montado requieren `Authorization: Bearer <http.token>` cuando `http.token` está fijado. Sin token, solo se permiten binds de loopback. | — | — |
| `--mode server` | — | — | `/health` sin autenticar; cada petición `/api/*` y `/mcp` requiere el bearer estático configurado (`http.token` / `CORTEX_HTTP_TOKEN`) antes de atarse al principal sintético constante. | Bearer estático |
| `mcp` (stdio local) | **Sin auth** (con ámbito de proceso) | — | — | stdio; sin credencial |
| `mcp` (proxy remoto, `mcp.remote.enabled`) | — | — | — | Bearer leído de `mcp.remote.token_env` (default `CORTEX_REMOTE_TOKEN`) |
| `sync --remote` | — | — | — | Bearer de `sync.token_env` (default `CORTEX_REMOTE_TOKEN`), con fallback a `http.token` |
| `web key` (web UI embebida) | Clave de acceso web en `<config dir>/web.key` (independiente de `http.token`) | igual | igual | — |
| `auth login` / `status` / `logout` | Gestiona `http.token` en sí; sin auth para ejecutar | — | — | — |

Notas:

- **Modo local zero-auth.** Los comandos SQLite locales abren el store directamente
  y nunca requieren una credencial.
- **`http.token` local.** Cuando está fijado, el HTTP local y el MCP montado
  requieren el bearer. Un `http.host` no-loopback sin `http.token` se rechaza en el
  arranque de `serve`.
- **Bearer estático del servidor.** Credencial única, verificada en cada petición
  autenticada; el ámbito tenant/workspace es configuración, no entrada del cliente.
- **Web embebida.** La clave de acceso web es un namespace distinto; rotarla nunca
  cambia `http.token`.
- **Perfiles MCP.** `agent`, `dev` y `minimal` son los catálogos vivos. La selección
  de perfil no cambia la autenticación.

---

## 6. Superficie obsoleta y retirada

Este apéndice registra superficie retirada u obsoleta para que los usuarios en
migración encuentren un aviso explícito y un reemplazo soportado.

### 6.1 Toolsets MCP retirados: `admin` y `temporal`

Los toolsets `admin` (borrado destructivo, fusión de proyectos, compaction) y
`temporal` (duración de ejecución, telemetría de memoria, timestamps manuales
RFC3339) están retirados del descubrimiento estándar de agentes y no deben ser
anunciados por ninguna sección de comando viva.

- **Reemplazo:** las operaciones administrativas usan la CLI (`cortex gc`,
  `cortex merge-projects`, `cortex doctor`), la TUI y el web dashboard; telemetría y
  salud usan `/health`; la evolución temporal se maneja automáticamente a través de
  upserts por `topic_key` y `cortex_revision_history`.
- El catálogo `mcp` vivo es exactamente `agent`, `dev` y `minimal`.

### 6.2 Imagen de contenedor retirada: `cortex-web`

La imagen de contenedor independiente `cortex-web` está retirada.

- **Reemplazo:** la web UI embebida la monta directamente `cortex serve` (local) y
  `cortex --mode server` (servidor), y su clave de acceso se gestiona con
  [`cortex web key`](#326-web).

### 6.3 Namespace de herramientas retirado: `mem_*` y el framing de era Engram

El namespace de herramientas `mem_*` y el framing de compatibilidad de era Engram
están retirados.

- **Reemplazo:** el namespace soportado es `cortex_*`. Los IDs de catálogo local y
  la superficie de era Engram no son intercambiables; ver [MCP.md](MCP.md).

### 6.4 Clave de configuración retirada: `multi_tenant`

La clave de configuración `server.multi_tenant` está eliminada. El servidor
self-hosted es single-tenant: tenant y workspace son constantes de configuración.

- **Reemplazo:** `server.tenant_id` y `server.workspace_id`. Un fichero que aún
  lleve `multi_tenant` carga sin error (la clave es desconocida y se descarta en el
  siguiente guardado), pero no tiene ningún efecto.

### 6.5 Migraciones raíz v1 retiradas

Los ficheros raíz `migrations/001-014` son historia v1 retirada y no impulsan el
arranque.

- **Reemplazo:** la línea base embebida y forward-only `migrations/v2/001_init.sql`
  (SQLite) y `migrations/v2/100_server.sql` (PostgreSQL). Las bases de datos v1 o
  ajenas existentes se rechazan sin mutarlas; no hay upgrade automático a v1. Usa
  [`cortex backup`](#320-backup) antes de cualquier migración.
