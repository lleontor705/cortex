# Instalación

## Requisitos

- Go 1.26.5 para compilaciones desde el código fuente.
- PostgreSQL 16 o superior para el modo servidor.
- Docker para el stack de servidor reproducible.
- `curl` y `jq` solo al usar la integración del hook de Claude Code.

## Homebrew (macOS / Linux)

```bash
brew install lleontor705/tap/cortex
```

## Go Install

```bash
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest
```

## Binario de release

Descarga el archivo comprimido de tu sistema operativo desde la página de [Releases](https://github.com/lleontor705/cortex/releases) del proyecto. Los artefactos de release habilitan la build tag `cortex_vectors`.

## Compilación desde el código fuente

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build
```

Esto instala la compilación por defecto con stub de vectores zero-CGO. Para un adaptador de vectores SQLite BLOB funcional local, compila con la build tag `cortex_vectors`:

```bash
go build -tags cortex_vectors ./cmd/cortex
```

## Configuración local

```bash
cortex setup claude-code
cortex setup opencode
cortex doctor
cortex search "example query"
```

La base de datos local usa `~/.cortex/cortex.db` por defecto. La configuración se lee de `~/.cortex/cortex.yaml` si existe.

`cortex setup opencode` instala su plugin de eventos de TypeScript a partir del contenido incrustado en el binario. El plugin nativo de ciclo de vida de Claude Code se instala por separado a través de su marketplace; `cortex setup claude-code` configura MCP y los permisos de herramientas.

## Servidor Docker (imágenes de GHCR)

Cortex proporciona una única imagen de contenedor oficial y multiarquitectura (`linux/amd64`, `linux/arm64`) alojada en **GitHub Container Registry (`ghcr.io`)**:

- **Cortex Server (API + web UI incrustada)**: `ghcr.io/lleontor705/cortex:latest`

El mismo binario sirve la web UI del operador en `/` y la API HTTP en `/api/*`, así que no hay una imagen web separada ni un segundo puerto de UI.

### Ejecución con Docker Compose

```bash
# 1. Configura las variables de entorno (overrides opcionales)
cp .env.example .env

# 2. Descarga e inicia PostgreSQL y Cortex Server
docker compose up -d
```

El servidor escucha en `http://localhost:7438` y sirve la web UI incrustada desde el mismo origen en `http://localhost:7438/`. El modo servidor es single-tenant: en el primer arranque el entrypoint de Docker aprovisiona y persiste una identidad de despliegue estable — las constantes `CORTEX_SERVER_TENANT_ID`, `CORTEX_SERVER_WORKSPACE_ID` y `CORTEX_SERVER_PRINCIPAL_SUBJECT`, más el bearer estático `CORTEX_HTTP_TOKEN` — generando cualquier valor que no hayas configurado. El primer arranque también crea una clave de acceso web y la imprime una sola vez. Copia ambas desde:

```bash
docker compose logs cortex-server
```

Abre `http://localhost:7438/` y pega la clave de acceso web una vez; el navegador la guarda y no vuelve a preguntar. La clave tiene su propio namespace y nunca es aceptada por `/api/*`, que sigue usando el bearer `CORTEX_HTTP_TOKEN`. Consulta [embedded-web.md](embedded-web.md) para el ciclo de vida completo de la clave.

### Ejecución como contenedor independiente

```bash
docker run -d \
  --name cortex-server \
  -p 7438:7438 \
  -v cortex-server-state:/home/cortex/.cortex \
  -e CORTEX_SERVER_AUTO_BOOTSTRAP=true \
  -e CORTEX_SERVER_STORAGE_DRIVER=postgres \
  -e CORTEX_SERVER_STORAGE_DSN="postgres://user:pass@host:5432/cortex?sslmode=disable" \
  -e CORTEX_SERVER_STORAGE_MIGRATION_DSN="postgres://admin:pass@host:5432/cortex?sslmode=disable" \
  ghcr.io/lleontor705/cortex:latest
```

Abre `http://localhost:7438/` y pega la clave de acceso web que se imprimió una vez en el primer arranque. El volumen `cortex-server-state` montado en `/home/cortex/.cortex` persiste el archivo de la clave (`web.key`) entre reinicios, de modo que el navegador no necesita volver a introducirla. Inspecciona o rota la clave desde dentro del contenedor:

```bash
docker exec cortex-server cortex web key show
docker exec cortex-server cortex web key regenerate
```

## Verificación

```bash
go test -v -count=1 -tags "integration postgres_integration" ./...
```

Los tests de integración de PostgreSQL requieren los DSN documentados en `AGENTS.md` y los roles de bootstrap de authz. No apuntes los tests de integración a una base de datos de producción compartida.
