# Web UI embebida

Cortex incluye la web UI del operador dentro del binario `cortex`. El mismo
proceso que sirve la API HTTP (`/api/*`) y la sonda de readiness (`/health`) sirve
también la exportación compilada de Next.js en `/`, así que **no hay contenedor
web separado ni un segundo puerto**. El handler Go en `internal/web` lee los
assets incrustados con `go:embed`, protege la superficie con una **clave de acceso
web** dedicada e inyecta el endpoint de API en runtime a través de `/config.js`.

> La clave de acceso web es una credencial en su propio namespace
> (`internal/webkey`). Nunca se usa como, se usa por defecto ni se deriva de
> `http.token` ni del token de replicación, y nunca la aceptan los endpoints
> `/api/*`.

## Inicio rápido

```bash
# 1. Boot the local SQLite server. The embedded UI is mounted on the HTTP listener.
cortex serve
```

En el muy primer arranque Cortex acuña una clave web y la imprime exactamente una
vez:

```text
Web access key generated on first boot.
<key>
Store it in a safe place; it will not be shown again.
mode=local
store=~/.cortex/cortex.db
sync=disabled
web=mounted
key_file=~/.cortex/web.key
listen=localhost:7438
```

```text
# 2. Open the UI and paste the key once.
http://localhost:7438/
```

La pantalla de entrada de clave guarda la clave en el navegador (clave de
localStorage `cortex_web_key`), sondea `/health` y después la superficie
protegida, y nunca vuelve a preguntar. Un pegado incorrecto se rechaza con un
error accionable que apunta a `cortex web key regenerate`.

Después del primer arranque el plaintext nunca se imprime de nuevo: los arranques
posteriores reutilizan en silencio la clave persistida. El listener por defecto es
`http://localhost:7438` (`http.host`/`http.port`); la superficie web hereda ese
host y puerto.

## Ciclo de vida de la clave

### Comandos

```bash
cortex web key show        # where the key lives, whether it exists, its prefix
cortex web key regenerate  # mint a replacement and print the new plaintext once
```

Ambos comandos aceptan una ruta de store explícita:

```bash
cortex web key show --key-file /path/to/web.key
cortex web key regenerate -k /path/to/web.key
```

`show` reporta la ruta del fichero de clave, si existe una clave y el prefijo
público; **nunca** imprime el plaintext. Cuando no existe ninguna clave reporta la
ausencia y el comportamiento de primer arranque sin crear una.

`regenerate` acuña una clave nueva, la persiste atómicamente e imprime el nuevo
plaintext exactamente una vez:

```text
Web access key regenerated.
Key file:       ~/.cortex/web.key

New web access key (shown once):
<key>

Store it in a safe place; it will not be shown again.
The previous key is no longer valid.
```

### Formato en reposo

El store de claves persiste solo un registro no-secreto — nunca el plaintext — en
`web.key` (default: `~/.cortex/web.key`):

```text
prefix=ctx_XXXXXXXX
digest=<base64url-hmac-sha256>
```

- El secreto plaintext son 32 bytes aleatorios de `crypto/rand`, codificados como
  `ctx_` + base64 (alfabeto URL raw). La línea `prefix` son los primeros 12
  caracteres de ese secreto; `digest` es un HMAC-SHA256 sobre el secreto con una
  clave derivada del prefijo.
- La verificación usa lookup por prefijo seguido de una comparación en tiempo
  constante (`hmac.Equal`), de modo que un digest manipulado se rechaza de forma
  determinista.
- El fichero se crea con modo `0600` dentro de un directorio `0700`. En sistemas
  POSIX un fichero de clave legible por grupo/otros falla en modo cerrado al
  cargar (los permisos no se reparan en silencio); Windows omite la comprobación
  de modo POSIX.
- Un fallo de escritura en el primer arranque nunca deja un fichero parcial.

### Semántica de rotación

- El primer arranque usa una creación exclusiva (`O_EXCL`), así que los arranques
  concurrentes acuñan exactamente una clave: el ganador imprime el plaintext y el
  perdedor carga la clave del ganador sin imprimir nada.
- `regenerate` escribe un fichero temporal en el mismo directorio, le hace fsync y
  lo renombra atómicamente sobre `web.key`. Si cualquier paso falla, la clave
  anterior sigue válida y sin cambios y el comando sale con código distinto de cero.

## Modos

La tríada de modos es un conjunto cerrado: `local`, `hybrid`, `server`
(`internal/platform/mode.go`). La superficie web embebida se monta en los tres; lo
que cambia es el store subyacente, la autorización de la API y cómo se conecta la
superficie.

| | `--mode local` (default) | `--mode hybrid` | `--mode server` |
|---|---|---|---|
| Store subyacente | SQLite (`~/.cortex/cortex.db`) | SQLite (composición idéntica) | PostgreSQL (self-hosted single-tenant) |
| Superficie web | Montada en `/` en el listener HTTP local | Montada en `/` en el listener HTTP local | Montada en `/` en el listener HTTP del servidor |
| Auth de API | Bearer estático `http.token` | Bearer estático `http.token` | Bearer estático `http.token` + principal sintético + stores RLS/autorizados |
| Endpoints de datos | `/api/*` (SQLite), incluyendo las cinco rutas de paridad de abajo | `/api/*` (SQLite) + bucle de replicación | Las mismas cinco rutas de paridad vía `internal/api` + MCP Streamable HTTP `/mcp` |
| Fichero de clave web | `~/.cortex/web.key` (o `--key-file`) | Igual que local | Misma ruta por defecto |
| Banner de estado | `mode=local store=<path> sync=<enabled\|disabled> web=mounted` | `mode=local … sync=enabled` (hybrid fija `CORTEX_MODE=hybrid`) | `cortex: web <baseURL>/`, `cortex: web key file <path>` |

- **local** — la composición SQLite de binario único con el servidor MCP stdio
  local. `cortex serve` arranca la API HTTP y monta la UI embebida en el mismo
  listener. Hybrid *no* es un store distinto: es esta composición con el bucle de
  sync/replicación remoto encima. Si se selecciona `--mode hybrid` pero el sync
  está deshabilitado o sin configurar, el arranque degrada a comportamiento local
  con un warning explícito que nombra la configuración de sync ausente.
- **server** — la composición PostgreSQL self-hosted single-tenant. `cmd/cortex`
  (el único puente local→servidor permitido) construye el mismo handler de
  `internal/web` y lo monta delante de las rutas autenticadas `/api/` y `/mcp`. El
  plano de peticiones autentica un bearer estático configurado (`http.token`)
  contra un principal de servicio sintético constante ensamblado a partir de
  `server.tenant_id`, `server.workspace_id` y `server.principal_subject`; no hay
  derivación de tenant o workspace por petición. La credencial se acuña solo
  después de que la composición del servidor abre, así que un bootstrap fallido
  nunca crea un fichero de clave ni reclama una superficie. Tenant y workspace
  siguen siendo constantes de configuración impuestas a través de los stores
  autorizados del servidor y el RLS forzado; la clave web es una credencial de UI
  independiente.

Los cinco endpoints críticos para la web los sirve el mux local `serve` detrás del
mismo gate de token `withAuth`, respaldado por un puerto del bundle sobre la
composición SQLite (REQ-SH-012):

- `GET /api/me` — principal local sintético (rol owner, modo local single-user).
- `GET /api/stats` — contadores recomputados desde el bundle local.
- `GET /api/projects` y `GET /api/agent/projects` — derivados de sesiones,
  observaciones y código.
- `GET /api/graph/project-graph` — BFS sobre edges de grafo respetando `depth` y
  `max_nodes`.

`/api/audit`, `/api/system/metrics` y `/api/rag/stats` siguen siendo items de
roadmap diferidos y **no** están registrados en el mux local (no existe store de
auditoría local).

Puesto que hybrid y local montan la superficie de forma idéntica, una sesión de
navegador que funciona contra un servidor local también funciona contra uno híbrido;
la UI simplemente ve el estado de replicación a través de la API.

## Inyección del endpoint en runtime

El servidor Go sirve el origen de API al navegador como un script, así que la UI no
está fijada a una URL de build-time:

- `GET /config.js` — sin autenticar, `application/javascript`, `no-cache`;
  define `window.__CORTEX_WEB_CONFIG__`.
- Por defecto (composiciones distribuidas) resuelve el origen de la página,
  manteniendo la superficie same-origin:
  `window.__CORTEX_WEB_CONFIG__ = { "serverUrl": window.location.origin };`.
- El cliente (`web/src/lib/server-endpoint.ts`, `resolveServerEndpoint`) lee
  `window.__CORTEX_WEB_CONFIG__.serverUrl` cuando está presente y si no cae al
  default documentado `http://localhost:7438`.
- `/health` se sirve sin autenticar para que el shell pueda renderizar antes de
  introducir una clave. `/api/*` nunca lo responde el handler web — el mux de API
  es dueño de ese namespace y conserva su propia semántica de bearer.

## Para contribuyentes

`web/` es una app Next.js exportada estáticamente (`output: "export"`), no
ejecutada como servidor Node. El pipeline de build está ordenado para que el binario
Go siempre compile, incluso en un checkout donde la toolchain web nunca corrió:

```bash
# Run this BEFORE make build / release builds.
make web-build
```

`make web-build`:

1. ejecuta `npm ci` solo cuando `web/node_modules` no existe,
2. ejecuta `npm run build` en `web/` (produciendo `web/out/`),
3. replica `web/out/` en `internal/web/dist/`, eliminando la salida de export
   previa pero **conservando el placeholder comprometido**
   `internal/web/dist/index.html`.

El placeholder mantiene `go:embed` y `go build ./...` en verde antes de cualquier
build web. `go:embed` usa el prefijo `all:` para que el árbol de assets `_next/`
se incruste intacto. `.gitignore` ignora el export sincronizado
(`/internal/web/dist/*`) mientras reincorpora el placeholder
(`!/internal/web/dist/index.html`), de modo que la salida masiva nunca se trackea.
Para el gate completo, ejecuta `make web-build` y después `make build`; los builds
de release siguen el mismo orden.

## Solución de problemas

- **Perdiste la clave.** Ejecuta `cortex web key regenerate` en el host y después
  pega la nueva clave en la UI. La clave anterior deja de verificar inmediatamente
  tras el reemplazo atómico; `cortex web key show` confirma dónde vive el fichero y
  su prefijo.
- **El servidor se niega a arrancar.** `cortex serve` se niega a exponer la API
  HTTP (y por tanto la superficie web) en un host no-loopback a menos que
  `http.token` esté configurado. Haz bind a `localhost` para uso local, o configura
  `http.token` intencionadamente.
- **La UI reporta una clave inválida.** La clave presentada no verificó
  (HTTP 401). El error inline apunta a `cortex web key regenerate`; el valor
  obsoleto no se almacena.
- **Error de permisos al cargar.** En POSIX el fichero de clave no debe ser legible
  por grupo/otros. Muévelo a un directorio privado y `chmod 600`; el store falla en
  modo cerrado en lugar de reparar el modo.
- **El arranque falla con "web access key unavailable".** No se pudo crear ni leer
  el store de claves (por ejemplo un directorio de datos no escribible). `serve` y
  `--mode server` abortan con código distinto de cero en lugar de servir
  silenciosamente una superficie solo-API.
