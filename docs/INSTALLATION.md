# Installation

## Homebrew (macOS / Linux)

```bash
brew install lleontor705/tap/cortex
```

Update:
```bash
brew update && brew upgrade cortex
```

## go install (Recommended for Windows)

```bash
go install github.com/lleontor705/cortex/cmd/cortex@latest
```

Binary goes to `$GOPATH/bin/cortex` (typically `~/go/bin/` or `%USERPROFILE%\go\bin\`).

## Build from Source

```bash
git clone https://github.com/lleontor705/cortex.git
cd cortex
go build -ldflags="-s -w" -o cortex ./cmd/cortex
```

With version stamp:
```bash
go build -ldflags="-s -w -X main.version=local-$(git describe --tags --always)" -o cortex ./cmd/cortex
```

## Pre-built Binaries

Download from [Releases](https://github.com/lleontor705/cortex/releases):

| Platform | File |
|----------|------|
| Linux x86_64 | `cortex_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | `cortex_<version>_linux_arm64.tar.gz` |
| macOS Intel | `cortex_<version>_darwin_amd64.tar.gz` |
| macOS Apple Silicon | `cortex_<version>_darwin_arm64.tar.gz` |
| Windows x86_64 | `cortex_<version>_windows_amd64.zip` |
| Windows ARM64 | `cortex_<version>_windows_arm64.zip` |

All releases include `checksums.txt` (SHA256).

### Linux / macOS

```bash
# Download (example: Linux x86_64)
curl -sSL https://github.com/lleontor705/cortex/releases/latest/download/cortex_linux_amd64.tar.gz | tar xz
chmod +x cortex
sudo mv cortex /usr/local/bin/
```

### Windows (PowerShell)

```powershell
Invoke-WebRequest -Uri https://github.com/lleontor705/cortex/releases/latest/download/cortex_windows_amd64.zip -OutFile cortex.zip
Expand-Archive cortex.zip -DestinationPath .
Move-Item cortex.exe C:\Users\$env:USERNAME\bin\
```

## Docker

```bash
docker build -t cortex .
docker run -v cortex-data:/root/.cortex cortex mcp
```

## Verify Installation

```bash
cortex version
cortex search "test"
```

## Integration Tests

The default test suite is database-independent. Generic integration tests use
the `integration` build tag; PostgreSQL tests use the dedicated
`postgres_integration` tag and require a reachable PostgreSQL 16 instance:

```bash
export CORTEX_TEST_POSTGRES_DSN='postgres://cortex_test:cortex_test@localhost:5432/cortex_test?sslmode=disable'
make test-integration
make test-postgres-coverage
```

The PostgreSQL harness fails when the DSN is missing or invalid; it never skips
database coverage silently.

## Agent Setup

After installing, configure your AI coding agent:

```bash
cortex setup claude-code
cortex setup opencode
cortex setup gemini-cli
cortex setup codex
```

See [AGENT-SETUP.md](AGENT-SETUP.md) for detailed per-agent instructions.

## Environment Variables

| Variable | Purpose | Default |
|----------|---------|---------|
| `CORTEX_PORT` | HTTP server port | `7438` |
| `CORTEX_DATABASE_PATH` | Database file location | `cortex.db` |
| `CORTEX_DATABASE_IN_MEMORY` | Use in-memory database | `false` |
| `CORTEX_LOGGING_LEVEL` | Log level (debug, info, warn, error) | `info` |

## Windows Notes

- `go install` is recommended to avoid antivirus false positives on unsigned binaries
- If using prebuilt binaries, you may need to add an antivirus exclusion
- Cortex uses pure Go SQLite (`modernc.org/sqlite`) — no C compiler or CGO needed
