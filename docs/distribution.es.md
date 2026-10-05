# Distribución

Cada canal de los siguientes instala el mismo binario `cortex` construido desde
este repositorio. Los artefactos de release, los builds desde fuente y
`make build` habilitan todos la build tag `cortex_vectors` (ruta funcional
zero-CGO de vectores BLOB de SQLite); consulta [Construir desde fuente](#construir-desde-fuente)
para lo que ocurre sin ella. Para los números de versión y artefactos actuales,
consulta siempre la [página de Releases](https://github.com/lleontor705/cortex/releases).

## Canales de un vistazo

| Canal | Comando | Gestionado por |
| --- | --- | --- |
| Homebrew (macOS / Linux) | `brew tap` + `brew install cortex` | GoReleaser → `lleontor705/homebrew-tap` |
| Go toolchain | `go install .../cmd/cortex@latest` | Tú |
| Binario de release | Descarga + verificación de checksum | GoReleaser (GitHub Releases) |
| Build desde fuente | `make build` | Tú |
| Imagen de contenedor | `docker pull ghcr.io/lleontor705/cortex` | Workflow de release (multi-arch) |

## Homebrew (macOS / Linux)

La automatización de release publica la fórmula en el repositorio
[`lleontor705/homebrew-tap`](https://github.com/lleontor705/homebrew-tap)
(GoReleaser `brews:` en `.goreleaser.yaml`, escrito en `Formula/cortex.rb`). La
copia de revisión dentro del repo vive en
[`packaging/homebrew/cortex.rb`](../packaging/homebrew/cortex.rb).

```bash
brew tap lleontor705/homebrew-tap
brew install cortex
```

La one-liner calificada por tap también funciona:

```bash
brew install lleontor705/tap/cortex
```

Actualiza más adelante con `brew update && brew upgrade cortex`. La fórmula
descarga el archivo de release precompilado para tu SO/arquitectura y verifica su
`sha256`; `brew install --HEAD cortex` construye en su lugar el commit `main`
más reciente desde fuente (requiere Go).

## Go Install

La ruta del módulo viene de `go.mod` (`github.com/lleontor705/cortex/v2`):

```bash
go install github.com/lleontor705/cortex/v2/cmd/cortex@latest
```

`go install` compila sin build tags, así que esta variante incluye el stub de
vectores degradado `sqlite_blob` (retrieval léxico FTS5 únicamente).
`cortex doctor` reporta el estado degradado. Habilita la ruta funcional de
vectores con:

```bash
go install -tags cortex_vectors github.com/lleontor705/cortex/v2/cmd/cortex@latest
```

## Binario de release

Cada [release](https://github.com/lleontor705/cortex/releases) contiene archivos
`cortex_<version>_<os>_<arch>.tar.gz` (`zip` para Windows) para macOS, Linux y
Windows en `amd64`/`arm64`, más un manifiesto `checksums.txt`.

```bash
curl -LO https://github.com/lleontor705/cortex/releases/download/<tag>/checksums.txt
curl -LO https://github.com/lleontor705/cortex/releases/download/<tag>/<archive>

# Linux (GNU coreutils)
sha256sum --ignore-missing -c checksums.txt

# macOS
grep '<archive>' checksums.txt | shasum -a 256 -c -
```

Después extrae el archivo y coloca `cortex` en tu `PATH`.

## Verificar firmas

Cada release lleva además un bundle de Sigstore para cada archivo y para
`checksums.txt` (`<asset>.sigstore`). Los bundles se producen sin claves
(keyless): el workflow de release se autentica mediante el token OIDC de GitHub,
Fulcio emite un certificado de corta vida y la entrada aterra en el log de
transparencia Rekor — este repositorio no almacena ninguna clave de firmado ni
secreto de firmado.

Instala [cosign](https://docs.sigstore.dev/cosign/system_config/installation/),
y después verifica el manifiesto de checksums:

```bash
curl -LO https://github.com/lleontor705/cortex/releases/download/<tag>/checksums.txt
curl -LO https://github.com/lleontor705/cortex/releases/download/<tag>/checksums.txt.sigstore

cosign verify-blob \
  --bundle checksums.txt.sigstore \
  --certificate-identity "https://github.com/lleontor705/cortex/.github/workflows/release.yml@refs/tags/<tag>" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt
```

Una vez que `checksums.txt` verifica, `sha256sum --ignore-missing -c checksums.txt`
(consulta [Binario de release](#binario-de-release)) autentica cada archivo que
lista. Un archivo individual también puede verificarse directamente sustituyendo
`<archive>` y su bundle `<archive>.sigstore`.

Notas:

- El bundle es autocontenido (certificado, firma y entrada de Rekor en un solo
  fichero), así que no se necesita ningún ajuste `COSIGN_REPOSITORY`: las firmas
  se distribuyen como assets de release en lugar de en un registro OCI.
- Un release cortado con `workflow_dispatch` firma desde la rama del dispatch, así
  que su identidad termina en `@refs/heads/<branch>`. Para aceptar cualquiera de
  las dos formas, sustituye `--certificate-identity` por
  `--certificate-identity-regexp 'https://github\.com/lleontor705/cortex/\.github/workflows/release\.yml@'`.
- Los SBOM SPDX (`<archive>.sbom.json`, generados por syft a través de GoReleaser)
  se adjuntan a la misma página de release.

## Construir desde fuente

Requisitos: Go (la directiva `toolchain` de `go.mod` fija la versión exacta que
CI hace cumplir) y Make.

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
make build        # outputs bin/cortex with -tags cortex_vectors (default)
make install      # optional: go install ./cmd/cortex into GOPATH/bin
```

`make build` pasa `-tags cortex_vectors` incondicionalmente, así que un
`make build` sencillo ya incluye el adaptador de vectores zero-CGO funcional. Un
`go build ./cmd/cortex` sin la tag a secas compila igualmente pero conecta el
stub de vectores degradado; `cortex doctor` explica la diferencia. CI, los
archivos de GoReleaser y el build head de Homebrew usan todos la misma
configuración con tag.

## Docker (GHCR, multi-arch)

El workflow de release publica la imagen del servidor (API HTTP + web UI embebida)
en GitHub Container Registry para `linux/amd64` y `linux/arm64`:

```bash
docker pull ghcr.io/lleontor705/cortex:latest
```

Los tags reflejan los metadatos del release: `latest`, el tag de git, la rama y
`sha-<commit>`. La imagen se construye desde `docker/Dockerfile`.
`docker compose up -d` usa la misma imagen; consulta
[INSTALLATION.md](INSTALLATION.md#servidor-docker-imagenes-de-ghcr) para las variables de
entorno, los puertos y el flujo de la clave de acceso web.

## Paquetes de plugins

Dos paquetes de plugins de editor/agente se distribuyen en este repositorio y se
verifican con sus propios harnesses:

```bash
# OpenCode plugin (TypeScript, Vitest; Node >= 24, own lockfile)
cd plugin/opencode && npm ci && npm test

# Claude Code plugin (hooks contract harness; needs bash, jq, python3, timeout)
bash plugin/claude-code/scripts/hooks_test.sh
```

Consulta [PLUGINS.md](PLUGINS.md) para la estructura del plugin y los lifecycle
hooks. Ninguno de los dos paquetes se publica en npm; se distribuyen desde este
repositorio.

## Convenciones de contribución y release

- **Hook pre-push**: Husky ejecuta `golangci-lint run ./...` seguido de
  `go test -v ./...`; un push falla rápido si cualquiera de los dos gates falla.
- **Pull requests**: los PRs se validan contra los targets `develop` y `master`.
  El body debe contener `Closes #N`, `Fixes #N` o `Resolves #N` para un issue con
  label `status:approved`, y el PR necesita exactamente un label `type:*`.
- **Releases**: los tag pushes ejecutan `.github/workflows/release.yml` —
  GoReleaser construye los archivos, `checksums.txt` y el commit del tap de
  Homebrew; el mismo workflow publica la imagen de GHCR.
