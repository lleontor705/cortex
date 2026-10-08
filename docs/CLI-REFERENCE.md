# Cortex CLI Reference

This is the single authoritative contract for the `cortex` command line binary
(`cmd/cortex`). Every command, flag, default, environment-variable override, exit
code, and authentication requirement documented here is anchored to the shipped
implementation: `cmd/cortex/main.go`, `internal/cli/cli.go`, `internal/cli/web.go`,
`internal/cli/watch.go`, `internal/platform/mode.go`, and `internal/config`.

If this document and any other repository document disagree about CLI behavior,
this document wins. Flag tables must exist in exactly one place, and that place is
here.

> **Related references.** Configuration keys and file formats live in
> [CONFIGURATION.md](CONFIGURATION.md). The MCP tool catalogs and profiles live in
> [MCP.md](MCP.md). Server deployment lives in [SERVER.md](SERVER.md). The HTTP
> REST surface lives in [HTTP-API.md](HTTP-API.md). Interactive terminal screens
> live in [TUI-GUIDE.md](TUI-GUIDE.md).

Sections: [invocation model](#1-invocation-model),
[global conventions](#2-global-conventions),
[command reference](#3-command-reference),
[environment-variable index](#4-environment-variable-index),
[authentication matrix](#5-authentication-matrix), and
[deprecated surface](#6-deprecated-and-retired-surface).

---

## 1. Invocation model

### 1.1 Binary and argument order

The production entrypoint is `cmd/cortex` (the `cortex` binary). The general form is:

```text
cortex [--mode local|hybrid|server] <command> [arguments...]
```

The binary first extracts and removes the global `--mode` flag, then dispatches on
the next positional argument (`args[1]`). Both flag syntaxes are accepted and the
flag may appear before or after the subcommand:

```text
cortex --mode hybrid search "release notes"
cortex --mode=hybrid search "release notes"
cortex search "release notes" --mode=hybrid
```

`--mode` parsing is strict. The valid set is exactly `local`, `hybrid`, and
`server`; any other value fails closed with exit code `2` and the error message:

```text
cortex: unknown mode "…": use --mode local, --mode hybrid, or --mode server
```

When `--mode` is absent the effective mode is `local`.

### 1.2 The mode triad

| Mode | Backend | Network surface | Notes |
|---|---|---|---|
| `local` | Single-binary SQLite (`~/.cortex/cortex.db`) with local stdio MCP | None required | Default. Byte-compatible with the standalone local composition. |
| `hybrid` | Local SQLite plus the remote sync/replication loop | Remote sync endpoint | Not a separate store: it is the local composition with replication layered on. The binary exports `CORTEX_MODE=hybrid` for the process. |
| `server` | Single-tenant PostgreSQL over authenticated HTTP and Streamable HTTP MCP | HTTP listener + `/mcp` | Wired by `cmd/cortex`; see [`--mode server`](#137-mode-server). |

`cortex status` (alias `cortex mode`) reports the active triad and its
description. Server-mode PostgreSQL is single-tenant; tenant and workspace scope
are configuration constants, never client input.

### 1.3 Exit-code contract

| Code | Meaning | Producing conditions |
|---|---|---|
| `0` | Success. | The command completed and printed its result. Commands that find no matching data (for example an empty search) still exit `0`. |
| `1` | Failure. | A runtime error (database, network, filesystem), a validation failure, an unknown command, or a per-command argument/usage error (missing required arguments, invalid numeric flag, invalid subcommand). Running `cortex` with no command also exits `1`. |
| `2` | Usage / argument error at the invocation layer. | An invalid `--mode` value, or a `--mode server` argument/configuration failure: unknown server argument, `--config` without a path, invalid `reindex --project-id`, server config that fails to load, or server bootstrap/web-key failure. |

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

> **Note.** Per-command usage errors (for example `cortex save` without a title and
> content, or `cortex config` without a subcommand) currently return `1`, not `2`.
> Only top-level mode parsing and the server invocation layer use `2`.

### 1.4 Bare interactive invocation → TUI

When the effective mode is `local` or `hybrid` and the process is invoked
interactively with no command arguments:

- the caller is an interactive terminal (`stdout` is a TTY and `stdin` is a TTY), and
- there are no command arguments,

then the binary rewrites the invocation to `cortex tui` and launches the
interactive terminal UI instead of printing usage. This covers double-clicking the
binary on Windows or running `cortex` directly in a terminal. Non-interactive
invocations (pipes, scripts, CI) still print usage and exit `1`.

### 1.5 Background update notice

Every dispatch attempt except `help` starts a background update check. The result
is printed to **stderr**, never stdout, so it cannot corrupt machine-readable
output:

```text
A new version of cortex is available: vX.Y.Z (current: vA.B.C)
<release-url>
```

- `cortex version` prints the notice synchronously.
- Other commands drain the background channel non-blockingly right before exiting,
  so the notice appears only when the check has already finished (best effort).
- `cortex update --check` performs a synchronous check instead.

### 1.6 `cortex serve` versus `--mode server`

`cortex serve` starts the **local** SQLite HTTP REST API and embedded web UI on the
configured listener (`http.host`/`http.port`, default `localhost:7438`). It is not
the PostgreSQL server.

`cortex --mode server` starts the **single-tenant PostgreSQL** composition with
authenticated `/api/*` and Streamable HTTP MCP at `/mcp`, plus the embedded web UI.
See [`--mode server`](#137-mode-server).

### 1.7 `--mode server`

```text
cortex --mode server [options]
cortex --mode server reindex --project-id <uuid> [options]
```

| Argument | Default | Environment override | Description |
|---|---|---|---|
| `--config <path>` | *(none)* | — | Path to the server YAML configuration file. Also accepts `--config=<path>`. |
| `reindex` | off | — | Run offline synchronous reindexing for one project, then exit. |
| `--project-id <uuid>` | *(required with reindex)* | — | Public project UUID to reindex. Must be a well-formed non-nil UUID. |
| `-h`, `--help`, `help` | — | — | Print server help and exit `0`. |
| `-v`, `--version`, `version` | — | — | Print the binary version and exit `0`. |

Server startup reads configuration from `--config` (or the default search paths),
opens the PostgreSQL composition, mints or loads the embedded web access key, and
prints the endpoint banner:

```text
cortex: server endpoint http://<host>:<port>
cortex: readiness http://<host>:<port>/health
cortex: API http://<host>:<port>/api/
cortex: MCP http://<host>:<port>/mcp
cortex: web http://<host>:<port>/
cortex: web key file <path>
```

On first boot the plaintext web access key is printed exactly once. `reindex`
prints a single summary line:

```text
cortex: server reindex complete project=<uuid> corpus=N upserted=N reembedded=N skipped=N batches=N
```

Exit codes: `0` success; `2` argument/config/bootstrap failure; `1` runtime
failure (`rt.Serve` error) or reindex execution error.

Authentication: the server verifies the configured static bearer
(`http.token` / `CORTEX_HTTP_TOKEN`) on every `/api/*` and `/mcp` request before
binding the synthetic constant principal. `/health` is unauthenticated. The
embedded web UI uses the separate web access key.

---

## 2. Global conventions

### 2.1 Flag styles

- **Value flags** accept both forms: `--limit 20` and `--limit=20`. Most commands
  implement the space-separated form; `--mode`, `--tools`, `--profile`, and the
  `code` subcommands also accept the `=` form. When a value flag is used without a
  value, the parser either ignores it or falls back to its default rather than
  consuming the next token as a value.
- **Boolean flags** are presence-only (`--json`, `--graph-expand`, `--staged`,
  `--regex`, `--pack`, `--dry-run`, `--check`, `--include-personal`, `--force`,
  `--to-obsidian`, `--remote`). There is no `--flag=false` form for them.
- **Aliases.** Several commands accept short aliases: `-p` (`--project`),
  `-m` (`--max-files`), `-f` (`--file`/`--force`, context dependent),
  `-t` (`--token`), `-k` (`--key-file`), `-c` (`--check`), `-o` (`--output`).
- Unknown tokens that do not start with `-` are treated as positional arguments
  (a query, a path, a target). Unknown tokens that do start with `-` are ignored
  by most parsers rather than rejected.

### 2.2 Defaults

Flag defaults documented in this reference are the effective CLI defaults in
`internal/cli`. They are independent of configuration defaults: for example
`cortex search` uses a hard-coded result limit of `10`, while the configuration
key `search.default_limit` defaults to `20`. When both exist, the CLI default
applies unless the flag is supplied.

### 2.3 Privacy preflight

`cortex save` and `cortex import` run a privacy preflight before persisting:

- All metadata fields (`project`, `topic_key`, `scope`, `type`, `source`,
  `session_id`, and tags) are validated with `privacy.ValidateMetadata`.
- `title` and `content` are passed through `privacy.ProtectField`.
- For batch import, the entire request is staged and validated against defensive
  copies; if any record fails, the import is rejected atomically with exit `1` and
  nothing is written.

### 2.4 Identifier spaces

- **Local mode** uses integer observation IDs (for example `#42`), opaque
  agent-provided session strings, and integer graph IDs.
- **Server mode** uses public UUIDs for observations and projects.
- The two spaces are not interchangeable. Numeric IDs from a local catalog must
  never be passed to server endpoints and vice versa.

### 2.5 Output streams

- Primary results go to **stdout**.
- Errors, warnings, and the background update notice go to **stderr**.
- `cortex serve` emits a pipe-safe `key=value` banner so its output stays
  machine-readable.

---

## 3. Command reference

Command inventory at a glance:

| Command | Purpose |
|---|---|
| [`help`](#31-help) | Print usage |
| [`version`](#32-version) | Print the binary version |
| [`tui`](#33-tui) | Launch the interactive terminal UI |
| [`serve`](#34-serve) | Start the local SQLite HTTP API + embedded web |
| [`mcp`](#35-mcp) | Start the local stdio MCP server |
| [`search`](#36-search) | Search observations |
| [`save`](#37-save) | Save an observation |
| [`context`](#38-context) | Show recent session context or a context pack |
| [`stats`](#39-stats) | Show memory statistics |
| [`timeline`](#310-timeline) | Show chronological context around an observation |
| [`revisions`](#311-revisions) | Show revision history for an observation |
| [`setup`](#312-setup) | Install agent integrations / configure Ollama |
| [`import`](#313-import) | Import observations from JSON |
| [`export`](#314-export) | Export observations to JSON or Obsidian |
| [`sync`](#315-sync) | Sync via file chunks or remote replication |
| [`merge-projects`](#316-merge-projects) | Merge project-name variants |
| [`reindex`](#317-reindex) | Rebuild vector embeddings |
| [`doctor`](#318-doctor) | Run health checks |
| [`gc`](#319-gc) | Garbage-collect archived observations |
| [`backup`](#320-backup) | Create an atomic SQLite snapshot |
| [`watch`](#321-watch) | Continuous file watcher for AST indexing |
| [`ingest`](#322-ingest) | One-shot AST ingestion |
| [`code`](#323-code) | Code AST intelligence (9 subcommands) |
| [`config`](#324-config) | Manage configuration |
| [`auth`](#325-auth) | Manage the local authentication token |
| [`web`](#326-web) | Manage the embedded web credential |
| [`status` / `mode`](#327-status--mode) | Display operational mode and status |
| [`migrate`](#328-migrate) | Manage database migrations |
| [`update`](#329-update) | Update Cortex to the latest release |

### 3.1 `help`

**Synopsis**

```text
cortex help
cortex --help
cortex -h
```

Prints the top-level usage/command list to stdout and exits `0`.

**Arguments:** none. **Flags:** none. **Exit codes:** `0`. **Authentication:** none.

**Example**

```bash
cortex help
```

**See also:** [version](#32-version).

### 3.2 `version`

**Synopsis**

```text
cortex version
cortex --version
cortex -v
```

Prints `cortex <version>` to stdout, drains the background update notice, and exits
`0`. The version is injected by GoReleaser via ldflags; when built from source it
falls back to the module version, or `dev`.

**Arguments:** none. **Flags:** none. **Exit codes:** `0`. **Authentication:** none.

**Example**

```bash
cortex version
# cortex v2.4.0
```

**See also:** [update](#329-update).

### 3.3 `tui`

**Synopsis**

```text
cortex tui [--config]
```

Launches the Bubble Tea interactive terminal UI.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--config` | off | — | Open the Configuration Center screen directly instead of the dashboard. |

**Arguments:** none. **Exit codes:** `0` success, `1` on app-open or program error.
**Authentication:** none (local store).

**Examples**

```bash
cortex tui
cortex tui --config
```

A bare interactive `cortex` invocation is rewritten to `cortex tui` (see
[§1.4](#14-bare-interactive-invocation--tui)).

**See also:** [TUI-GUIDE.md](TUI-GUIDE.md), [`config wizard`](#324-config).

### 3.4 `serve`

**Synopsis**

```text
cortex serve
```

Starts the local SQLite HTTP REST API (`/api/*`) and, when the web surface mounts,
the embedded web UI. The listen address is `http.host:http.port`.

`serve` takes no flags; the trailing arguments are ignored.

| Setting | Default | Environment override | Description |
|---|---|---|---|
| `http.host` | `localhost` | `CORTEX_HTTP_HOST` | Listen interface. Empty/`localhost`/loopback is treated as local. |
| `http.port` | `7438` | `CORTEX_HTTP_PORT` | Listen port. |
| `http.token` | *(empty)* | `CORTEX_HTTP_TOKEN` | Static bearer. Required for non-loopback binds. |
| `http.allowed_origins` | `[]` | `CORTEX_HTTP_ALLOWED_ORIGINS` | CORS browser origins. |

On first boot the web access key is minted and printed exactly once. The banner:

```text
mode=local
store=<database path>
sync=disabled|enabled
web=mounted|disabled
key_file=<path>
listen=<host>:<port>
```

**Exit codes:** `0` success; `1` if the app fails to open, the non-loopback bind
has no `http.token`, the web key cannot be resolved, or the listener fails.
**Authentication:** none by default; when `http.token` is set, `/api/*` and the
mounted MCP require `Authorization: Bearer <token>`. `/health` stays
unauthenticated.

**Examples**

```bash
cortex serve
CORTEX_HTTP_TOKEN=secret CORTEX_HTTP_HOST=0.0.0.0 cortex serve
```

**See also:** [HTTP-API.md](HTTP-API.md), [web key](#326-web), [`--mode server`](#17-mode-server).

### 3.5 `mcp`

**Synopsis**

```text
cortex mcp [--tools=PROFILE] [--profile=PROFILE]
```

Starts the local stdio MCP server.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--tools[=]PROFILE` | `mcp.profile` (default `agent`) | `CORTEX_MCP_PROFILE` | Tool catalog: `agent` (22 tools), `dev` (11 tools), `minimal` (5 tools). |
| `--profile[=]PROFILE` | *(same as `--tools`)* | `CORTEX_MCP_PROFILE` | Alias for `--tools`. |

When `mcp.remote.enabled` is true, `cortex mcp` does not open SQLite; it proxies
the configured remote server and forwards calls over the local stdio transport.
In that mode the remote server owns the catalog and local profiles do not filter
it.

**Exit codes:** `0` success; `1` on config load, app-open, or serve failure.
**Authentication:** none locally (process-scoped stdio). A remote proxy reads its
bearer from `mcp.remote.token_env` (default `CORTEX_REMOTE_TOKEN`).

**Examples**

```bash
cortex mcp
cortex mcp --tools=minimal
cortex mcp --profile dev
```

The `admin` and `temporal` toolsets are retired; see
[§6](#6-deprecated-and-retired-surface).

**See also:** [MCP.md](MCP.md), [`config`](#324-config).

### 3.6 `search`

**Synopsis**

```text
cortex search <query> [flags]
```

Searches observations with multi-modal retrieval.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `<query>` | yes | Query text. Multiple non-flag tokens are joined with spaces. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--mode[=]MODE` | `auto` | — | Retrieval mode: `auto`, `direct`, `semantic`, `multi_hop`. `multi_hop` enables graph expansion; `direct` disables it. |
| `--graph-expand` | off | — | Force graph expansion on. |
| `--type TYPE` | *(empty)* | — | Filter by observation type. |
| `--project PROJECT` | *(empty)* | — | Filter by project. |
| `--scope SCOPE` | *(empty)* | — | Filter by scope. |
| `--limit N` | `10` | — | Maximum results. Must be an integer; invalid values exit `1`. |

**Exit codes:** `0` success (including zero matches, which prints
`No memories found for: "<query>"`); `1` for a missing/blank query, invalid
`--limit`, or store error. **Authentication:** none.

**Examples**

```bash
cortex search "release process"
cortex search "graph ranking" --mode=multi_hop --limit 5
cortex search "auth" --type decision --project cortex --scope project
```

**See also:** [context](#38-context), [CONFIGURATION.md](CONFIGURATION.md).

### 3.7 `save`

**Synopsis**

```text
cortex save <title> <content> [flags]
```

Saves a local observation after the privacy preflight.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `<title>` | yes | Observation title. Must be non-blank. |
| `<content>` | yes | Observation body. Must be non-blank. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--type TYPE` | `manual` | — | Observation type. |
| `--project PROJECT` | *(empty)* | — | Project name. |
| `--scope SCOPE` | `project` | — | Observation scope. |
| `--topic TOPIC_KEY` | *(empty)* | — | Stable topic key for dedupe/upsert. |

The session ID defaults to `manual-save` (or `manual-save-<project>` when a
project is given).

**Exit codes:** `0` success; `1` on validation/privacy rejection or store error.
**Authentication:** none.

**Examples**

```bash
cortex save "Release checklist" "Tag, build, publish, announce"
cortex save "Auth model" "Static bearer only" --type decision --topic architecture/auth-model --project cortex
```

**See also:** [import](#313-import), privacy preflight [§2.3](#23-privacy-preflight).

### 3.8 `context`

**Synopsis**

```text
cortex context [project] [flags]
```

Shows recent session context, or renders a context pack when a format is selected.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `[project]` | no | Project filter. The first non-flag token that is not a flag value. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--scope SCOPE` | *(empty)* | — | Filter by scope. |
| `--format FORMAT` | *(empty)* | — | Render a context pack in the given format (for example `markdown`, `json`). Also accepts `--format=FORMAT`. |
| `--pack` | off | — | Shorthand: select the `markdown` pack format. |
| `--budget=N` | `0` | — | Include the repository map with an N-token budget (enables repo-map only when `> 0`). |

Without `--format`/`--pack`, prints up to 5 recent sessions and up to 20 recent
observations.

**Exit codes:** `0` success (including no prior memories); `1` on render or store
error. **Authentication:** none.

**Examples**

```bash
cortex context
cortex context cortex --scope project
cortex context cortex --pack --budget=4096
```

**See also:** [search](#36-search).

### 3.9 `stats`

**Synopsis**

```text
cortex stats
```

Prints sessions, observations, and project totals. Arguments are ignored.

**Flags:** none. **Exit codes:** `0` success, `1` store error.
**Authentication:** none.

**Example**

```bash
cortex stats
# Cortex Memory Stats
#   Sessions:     4
#   Observations: 128
#   Projects:     cortex
```

### 3.10 `timeline`

**Synopsis**

```text
cortex timeline <observation_id> [--before N] [--after N]
```

Prints observations adjacent in time around the target, with the target
highlighted.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `<observation_id>` | yes | Integer observation ID. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--before N` | `3` | — | Older neighbors to show. Must be an integer. |
| `--after N` | `3` | — | Newer neighbors to show. Must be an integer. |

**Exit codes:** `0` success; `1` for missing/invalid ID, invalid counts, or store
error. **Authentication:** none.

**Examples**

```bash
cortex timeline 42
cortex timeline 42 --before 5 --after 10
```

**See also:** [revisions](#311-revisions).

### 3.11 `revisions`

**Synopsis**

```text
cortex revisions <observation_id> [--limit N]
```

Prints the temporal revision history for an observation.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `<observation_id>` | yes | Integer observation ID. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--limit N` | `20` | — | Maximum revisions to print. Non-positive values fall back to `20`. |

**Exit codes:** `0` success (including `No revision history found`);
`1` for missing/invalid ID or store error. **Authentication:** none.

**Examples**

```bash
cortex revisions 42
cortex revisions 42 --limit 100
```

### 3.12 `setup`

**Synopsis**

```text
cortex setup [agent] [flags]
cortex setup ollama [--base-url=URL] [--model=NAME]
```

With no agent argument, prints detected agents and quick commands.

**Arguments**

| Argument | Required | Description |
|---|---|---|
| `[agent]` | no | One of `opencode`, `claude-code`, `gemini-cli`, `codex`, `ollama`. |

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--profile[=]P` / `-p P` | `mcp.profile` (default `agent`) | `CORTEX_MCP_PROFILE` | Install profile: `dev` (11 tools), `minimal` (5 tools), `agent` (22 tools). |
| `--base-url=URL` | `http://localhost:11434` | — | `setup ollama` only: Ollama base URL. |
| `--model=NAME` | `nomic-embed-text` | — | `setup ollama` only: embedding model to configure. |

**Exit codes:** `0` success (including detection-only mode); `1` on install or
Ollama configuration failure. **Authentication:** none.

**Examples**

```bash
cortex setup
cortex setup claude-code --profile=dev
cortex setup ollama --model=nomic-embed-text
```

**See also:** [status](#327-status--mode), [MCP.md](MCP.md).

### 3.13 `import`

**Synopsis**

```text
cortex import --from-json --path FILE
```

Imports observations from a JSON array file. The input is capped at **50 MiB**
(`50 << 20`); the entire batch is privacy-validated before any write.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--from-json` | *(required first token)* | — | Select the JSON import source. |
| `--path FILE` | *(required)* | — | Input JSON file path. |

Records whose save fails are skipped with a warning; the command still exits `0`
and reports `Imported <saved> of <total> observations from JSON`.

**Exit codes:** `0` success; `1` for a missing source/`--path`, unreadable file,
invalid JSON, privacy rejection, or app-open error. **Authentication:** none.

**Examples**

```bash
cortex export --output backup.json
cortex import --from-json --path backup.json
```

**See also:** [export](#314-export), privacy preflight [§2.3](#23-privacy-preflight).

### 3.14 `export`

**Synopsis**

```text
cortex export [--project P] [--output FILE]
cortex export --to-obsidian --vault PATH [flags]
```

Exports observations to JSON (stdout or a file) or to an Obsidian read-only
projection.

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--project P` | *(empty)* | — | Filter by project. Also accepts `--project=P`. |
| `--output FILE` | *(stdout)* | — | Write JSON to `FILE` (mode `0600`). Also accepts `--output=FILE`. |
| `--to-obsidian` | off | — | Switch to the Obsidian projection. |
| `--vault PATH` | *(required with `--to-obsidian`)* | — | Target vault directory. |
| `--include-personal` | off | — | Include personal-scope observations in the projection. |

The JSON export lists up to 10,000 observations.

**Exit codes:** `0` success; `1` for store/write/marshal errors, or
`--to-obsidian` without `--vault`. **Authentication:** none.

**Examples**

```bash
cortex export --project cortex --output cortex.json
cortex export --to-obsidian --vault ~/vault/synapse --project cortex
```

**See also:** [import](#313-import).

### 3.15 `sync`

**Synopsis**

```text
cortex sync [--import | --status | --all] [--project P]
cortex sync --remote
```

Synchronizes observations through file chunks (default: export) or remote
replication.

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--import` | off | — | Import pending chunks instead of exporting. |
| `--status` | off | — | Print local/remote chunk counts and pending imports. |
| `--all` | off | — | Export all projects (no project auto-detection). |
| `--project P` | *(auto-detected)* | — | Restrict to a project. |
| `--remote` | off | — | Replicate against the configured remote endpoint (`sync.url`). |

Default (no `--import`/`--status`/`--remote`): exports a chunk for the detected
project. `--remote` requires `sync.url`; the bearer is resolved from
`sync.token_env` (default `CORTEX_REMOTE_TOKEN`), falling back to `http.token`.

**Exit codes:** `0` success; `1` for missing remote URL, sync errors, or app-open
failure. **Authentication:** remote replication requires a bearer; file transport
requires none.

**Examples**

```bash
cortex sync --status
cortex sync --all
cortex sync --remote
```

**See also:** [CONFIGURATION.md](CONFIGURATION.md).

### 3.16 `merge-projects`

**Synopsis**

```text
cortex merge-projects --from "Name1,Name2" --to canonical-name [--dry-run]
```

Merges project-name variants into one canonical name.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--from LIST` | *(required)* | — | Comma-separated source names. |
| `--to NAME` | *(required)* | — | Canonical target name. |
| `--dry-run` | off | — | Print the planned merge without writing. |

**Exit codes:** `0` success (including dry run); `1` for missing `--from`/`--to`
or merge error. **Authentication:** none.

**Examples**

```bash
cortex merge-projects --from "cortex,cortex-ia" --to cortex --dry-run
cortex merge-projects --from "cortex,cortex-ia" --to cortex
```

### 3.17 `reindex`

**Synopsis**

```text
cortex reindex [--project P]
```

Regenerates vector embeddings for observations using the configured embedding
provider. Requires a configured embedding provider and a healthy vector store
(build with `-tags cortex_vectors` for the SQLite BLOB adapter). Processes up to
**5000** observations per run and prints progress every 50.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--project P` | *(all projects)* | — | Restrict reindexing to one project. |

When the provider is `ollama` and `search.ollama_auto_start` is true, the command
starts Ollama and pulls the model if missing.

**Exit codes:** `0` success (including zero observations); `1` when no embedding
provider is configured, the vector store is unavailable, or an error occurs.
**Authentication:** none.

**Examples**

```bash
cortex reindex
cortex reindex --project cortex
```

**See also:** [`code scan`](#323-code), [CONFIGURATION.md](CONFIGURATION.md).

### 3.18 `doctor`

**Synopsis**

```text
cortex doctor
cortex doctor --server [URL]
```

Runs local health checks, or probes a running server when `--server` is given.

**Flags**

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--server [URL]` | off; URL default `http://localhost:7438` | `CORTEX_HTTP_TOKEN` (for the bearer probe) | Probe a server instead of the local store. If the next token does not start with `-`, it is used as the base URL. |

Local checks: operating mode, database, FTS5 search, knowledge graph, vector
store, embeddings, orphan ratio, code AST index, Ollama daemon, and a
vendor-key isolation notice. Any failing check counts as an issue and the command
exits `1`.

Server checks: `/health` (HTTP 200), optional bearer verification against `/mcp`
when `CORTEX_HTTP_TOKEN` (or `CORTEX_REMOTE_TOKEN`) is set, and an embedded web UI
probe. A server-side probe failure is informational and does not change the
exit-code semantics of the informational line.

**Exit codes:** `0` all checks passed; `1` one or more issues.
**Authentication:** none locally; the server probe uses `CORTEX_HTTP_TOKEN`.

**Examples**

```bash
cortex doctor
cortex doctor --server
cortex doctor --server http://localhost:7438
```

**See also:** [`status`](#327-status--mode).

### 3.19 `gc`

**Synopsis**

```text
cortex gc [--days N]
```

Hard-deletes soft-deleted/archived observations older than `N` days (up to 1000
candidates per run).

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--days N` | `90` | — | Age cutoff in days. Only positive integers are accepted; invalid values are ignored. |

**Exit codes:** `0` success (including `Nothing to collect.`); `1` on list or
delete error. **Authentication:** none. **Warning:** this is a destructive
operation.

**Examples**

```bash
cortex gc
cortex gc --days 30
```

### 3.20 `backup`

**Synopsis**

```text
cortex backup [path]
```

Creates an atomic online snapshot of the SQLite database using `VACUUM INTO`.

| Argument | Default | Environment override | Description |
|---|---|---|---|
| `[path]` | `cortex-backup-<YYYYMMDD-HHMMSS>.db` | — | Destination path. An existing target is removed first. |

**Exit codes:** `0` success; `1` on path resolution, app-open, or backup error.
**Authentication:** none.

**Examples**

```bash
cortex backup
cortex backup ~/backups/cortex.db
```

**See also:** [migrate](#328-migrate).

### 3.21 `watch`

**Synopsis**

```text
cortex watch [path] [--project NAME]
```

Watches a repository and incrementally re-indexes AST symbols on file
create/change/delete until interrupted (Ctrl+C). Prints `👀 Cortex File Watcher
activo en …` on start and `🛑 File Watcher detenido.` on stop (the watcher's live
progress strings are currently Spanish; this is a known cosmetic quirk).

| Argument / Flag | Default | Environment override | Description |
|---|---|---|---|
| `[path]` | `.` | — | Directory to watch (resolved to an absolute path). |
| `--project NAME` / `--project=NAME` | auto-detected, else `default` | — | Project name for indexed symbols. |

**Exit codes:** `0` clean stop; `1` on directory resolution, app-open, or watcher
error. **Authentication:** none.

**Examples**

```bash
cortex watch
cortex watch ./internal --project cortex
```

**See also:** [ingest](#322-ingest), [`code scan`](#323-code).

### 3.22 `ingest`

**Synopsis**

```text
cortex ingest [path] [--project NAME] [--max-files N]
```

Scans a codebase once with the Zero-CGO static AST extractor, persists symbols and
relations, and prints Graphify analytics (files, cohesion, god nodes, import
cycles).

**Arguments / Flags**

| Argument / Flag | Default | Environment override | Description |
|---|---|---|---|
| `[path]` | `.` | — | Directory to scan. |
| `--project NAME` / `-p NAME` | auto-detected, else `default` | — | Project name. |
| `--max-files N` / `-m N` | `500` | — | Maximum files to scan. Only positive integers are accepted. |

**Exit codes:** `0` success; `1` on extraction, app-open, or save error.
**Authentication:** none.

**Examples**

```bash
cortex ingest
cortex ingest ./internal --project cortex --max-files 200
```

**See also:** [`code scan`](#323-code), [watch](#321-watch).

### 3.23 `code`

**Synopsis**

```text
cortex code <subcommand> [arguments] [flags]
```

Code AST intelligence. `cortex code`, `cortex code help`, `cortex code --help`,
and `cortex code -h` print the subcommand list and exit `0`.

Subcommands:

| Subcommand | Purpose |
|---|---|
| [`scan`](#code-scan) | Scan a repository and index AST symbols (same as `ingest`). |
| [`symbols`](#code-symbols) | List indexed symbols. |
| [`analyze`](#code-analyze) | Run Graphify architectural analytics. |
| [`impact`](#code-impact) | Calculate the blast radius of a symbol or file. |
| [`diff`](#code-diff) | Blast radius of uncommitted Git changes. |
| [`graph`](#code-graph) | Export/visualize the dependency graph. |
| [`map`](#code-map) | Generate a token-budgeted repo map. |
| [`tests`](#code-tests) | Find impacted test files/functions. |
| [`find`](#code-find) | Search symbols by substring or regex. |

All subcommands default `--project` to `default` unless noted.

#### `code scan`

```text
cortex code scan [path] [--project=NAME] [--max-files=N]
```

Delegates to [`ingest`](#322-ingest); identical flags and defaults
(`--max-files` default `500`).

#### `code symbols`

```text
cortex code symbols [--project=NAME] [--kind=KIND] [--file=PATH]
```

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--project=NAME` | `default` | — | Project name. |
| `--kind=KIND` | *(empty)* | — | Filter by symbol kind. |
| `--file=PATH` | *(empty)* | — | Filter by file path. |

Lists up to 100 symbols.

#### `code analyze`

```text
cortex code analyze [--project=NAME]
```

Prints totals, average cohesion, god nodes, and import cycles.

#### `code impact`

```text
cortex code impact <target> [--project=NAME] [--hops=N] [--json]
```

| Flags/Args | Default | Environment override | Description |
|---|---|---|---|
| `<target>` | *(required)* | — | Symbol name, ID, or file path. |
| `--project=NAME` | `default` | — | Project name. |
| `--hops=N` | `3` | — | Traversal depth. |
| `--json` | off | — | Emit JSON. |

`blast-radius` is an alias for `impact`.

#### `code diff`

```text
cortex code diff [--staged] [--project=NAME] [--hops=N] [--json]
```

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--staged` / `--cached` | off | — | Analyze the Git staging area instead of the working tree. |
| `--project=NAME` | `default` | — | Project name. |
| `--hops=N` | `3` | — | Traversal depth. |
| `--json` | off | — | Emit JSON. |

Requires `git` and a Git repository.

#### `code graph`

```text
cortex code graph [--project=NAME] [--format=mermaid] [--symbol=S] [--hops=N] [--max-nodes=N]
```

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--project=NAME` | `default` | — | Project name. |
| `--format=F` | `mermaid` | — | Output format: `mermaid`, `ascii` (alias `tree`), `json`. Unknown formats exit `1`. |
| `--symbol=S` | *(empty)* | — | Root symbol to center the graph on. |
| `--hops=N` | `2` | — | Traversal depth when a root symbol is given. |
| `--max-nodes=N` | `50` | — | Maximum nodes. |

#### `code map`

```text
cortex code map [--project=NAME] [--budget=N]
```

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--project=NAME` | `default` | — | Project name. |
| `--budget=N` | `2048` | — | Token budget. |

`repo-map` is an alias for `map`.

#### `code tests`

```text
cortex code tests <target> [--project=NAME] [--hops=N] [--json]
```

| Flags/Args | Default | Environment override | Description |
|---|---|---|---|
| `<target>` | *(required)* | — | Symbol name, ID, or file path. |
| `--project=NAME` | `default` | — | Project name. |
| `--hops=N` | `3` | — | Traversal depth. |
| `--json` | off | — | Emit JSON. |

`test-map` and `impacted-tests` are aliases for `tests`.

#### `code find`

```text
cortex code find <query> [--project=NAME] [--kind=K] [--file=PATH] [--limit=N] [--regex] [--json]
```

| Flags/Args | Default | Environment override | Description |
|---|---|---|---|
| `<query>` | *(required)* | — | Substring or regex query. |
| `--project=NAME` | `default` | — | Project name. |
| `--kind=K` | *(empty)* | — | Filter by symbol kind. |
| `--file=PATH` | *(empty)* | — | Filter by file path. |
| `--limit=N` | `50` | — | Maximum results. |
| `--regex` | off | — | Treat the query as a regular expression. |
| `--json` | off | — | Emit JSON. |

`search-symbols` is an alias for `find`.

**Exit codes:** `0` success; `1` for missing required targets/queries, unknown
formats, unknown subcommands, or store errors. **Authentication:** none.

**Examples**

```bash
cortex code scan . --project cortex
cortex code symbols --project cortex --kind func
cortex code impact CalculateBlastRadius --project cortex --hops=2
cortex code diff --staged --json
cortex code graph --project cortex --format=ascii --max-nodes=30
cortex code tests CalculateBlastRadius
cortex code find "RepoMap" --project cortex --regex
```

**See also:** [ingest](#322-ingest), [watch](#321-watch).

### 3.24 `config`

**Synopsis**

```text
cortex config <subcommand> [arguments] [flags]
```

Manages configuration without editing files by hand.

| Subcommand | Purpose |
|---|---|
| `get <key> [--file=PATH]` | Print one value. |
| `set <key> <value> [--file=PATH] [--format=FMT]` | Set a value and persist. |
| `show` / `print` | Print the active config (`--format` `yaml` default or `json`; secrets masked). |
| `validate` | Validate a config file. |
| `init` | Write a minimal config file. |
| `path` | Print the resolved config file path. |
| `wizard` / `interactive` | Launch the configuration wizard. |

| Flag | Applies to | Default | Environment override | Description |
|---|---|---|---|---|
| `--file PATH` / `-f` / `--config` | `get`, `set`, `show`, `validate` | *(discovered)* | — | Explicit config file. Also accepts `--file=PATH` / `--config=PATH`. |
| `--format FMT` | `show`, `init`, `set` | `yaml` | — | `yaml` or `json` for `show`; `yaml`/`json`/`toml` extension for `set`/`init`. |
| `--force` / `-f` | `init` | off | — | Overwrite an existing file. |
| `-o PATH` / `--output PATH` | `init` | *(default config dir)* | — | Output path. |
| `--cli` | `wizard` | off | — | Force the CLI wizard. |
| `--tui` | `wizard` | off | — | Force the TUI wizard. |

When `wizard` is run with neither flag, it launches the TUI wizard if stdout is a
terminal; otherwise it falls back to the CLI summary.

Settable keys (via `get`/`set`): `ai.provider` (alias `llm.provider`), `ai.model`,
`ai.base_url`, `database.path`, `database.in_memory`, `http.enabled`,
`http.port`, `http.host`, `http.token`, `logging.level`, `logging.format`,
`search.embedding_provider` (alias `embedding.provider`),
`search.embedding_model`, `search.embedding_base_url`, `search.vector`,
`search.fts5`, `search.ollama_auto_start`, `mcp.enabled`, `mcp.profile`,
`mcp.remote.enabled`, `mcp.remote.url`, `mcp.remote.token_env`, `sync.enabled`,
`sync.url`, `sync.token_env`, `sync.interval`, `memory.auto_archive_days`,
`memory.importance_decay_half_life`, `memory.min_archive_score`, `vector.provider`.

`config show` masks `http.token`, `vector.qdrant.api_key`,
`server.secrets.signing_key`, and `server.secrets.oidc_client_secret`.

**Exit codes:** `0` success; `1` for a missing/unknown subcommand or key,
invalid value, or load/save error. **Authentication:** none.

**Examples**

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

**See also:** [CONFIGURATION.md](CONFIGURATION.md).

### 3.25 `auth`

**Synopsis**

```text
cortex auth <login|status|logout> [options]
```

Manages the local authentication token stored in `http.token`.

| Subcommand | Flags | Behavior |
|---|---|---|
| `login` | `--token TOKEN` / `-t` / `--token=TOKEN` | Persists the token to the loaded config. Requires a non-empty token. |
| `status` / `whoami` | none | Prints mode, masked token, principal, and role. |
| `logout` | none | Clears `http.token` and saves. |

**Exit codes:** `0` success; `1` for a missing subcommand, a missing login token,
or a load/save error. **Authentication:** none (this command manages the
credential itself).

**Examples**

```bash
cortex auth login --token=<bearer>
cortex auth status
cortex auth logout
```

**See also:** [§5 Authentication matrix](#5-authentication-matrix).

### 3.26 `web`

**Synopsis**

```text
cortex web key show [--key-file PATH]
cortex web key regenerate [--key-file PATH]
```

Manages the embedded web access key (independent of `http.token`).

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--key-file PATH` / `-k` / `--key-file=PATH` | `<config dir>/web.key` | — | Key file location. |

- `show` reports whether a key exists, its file path, and its prefix. It never
  prints the plaintext.
- `regenerate` rotates the key atomically and prints the new plaintext exactly
  once; the previous key is invalidated.

**Exit codes:** `0` success; `1` for a missing subcommand, an unexpected
argument, or a key store error. **Authentication:** none.

**Examples**

```bash
cortex web key show
cortex web key regenerate
```

**See also:** [serve](#34-serve), [`--mode server`](#17-mode-server).

### 3.27 `status` / `mode`

**Synopsis**

```text
cortex status [--json]
cortex mode [--json]
```

Prints the operational mode, database path, sync status, embeddings, and config
path. `mode` is an alias for `status`.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--json` | off | — | Emit a JSON object instead of the human-readable block. |

The JSON shape is:

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

The effective mode is resolved from `CORTEX_MODE`, then `server.storage.dsn`, then
`sync.enabled` + `sync.url`, then `local`.

**Exit codes:** `0` success. **Authentication:** none.

**Examples**

```bash
cortex status
cortex status --json
cortex mode --json
```

**See also:** [doctor](#318-doctor), [§1.2 mode triad](#12-the-mode-triad).

### 3.28 `migrate`

**Synopsis**

```text
cortex migrate <up|down|status> [--target VERSION]
```

Manages database migrations.

| Subcommand | Behavior |
|---|---|
| `up` | Apply all pending migrations. |
| `down [--target N]` | Roll back to version `N` (default `0`). |
| `status` | Print each migration's version, name, and applied timestamp. |

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--target N` | `0` | — | Target version for `down`. |

The local v2 baseline is forward-only. `migrate down` is not a supported normal
operation and existing v2 databases must not be downgraded automatically.

**Exit codes:** `0` success; `1` for a missing/unknown subcommand, an invalid
target, or a migration error. **Authentication:** none.

**Examples**

```bash
cortex migrate status
cortex migrate up
```

**See also:** [backup](#320-backup).

### 3.29 `update`

**Synopsis**

```text
cortex update [--check]
```

Updates Cortex to the latest release, or checks for one.

| Flag | Default | Environment override | Description |
|---|---|---|---|
| `--check` / `-c` | off | — | Report the latest release without installing. |

When a newer release exists, `--check` prints the version, release notes, and
release page. Without `--check`, a self-update is attempted.

**Exit codes:** `0` success (including up-to-date); `1` when the update fails.
**Authentication:** none (uses the public release channel).

**Examples**

```bash
cortex update --check
cortex update
```

---

## 4. Environment-variable index

Cortex resolves configuration from the config file first, then from environment
variables. Every configuration key is automatically addressable as
`CORTEX_` + the upper-cased key with `.` replaced by `_` (viper
`AutomaticEnv` with the `CORTEX` prefix). For example `http.port` becomes
`CORTEX_HTTP_PORT`. The variables below are listed alphabetically; the
"Default" column is the effective default when neither a config file nor the
variable is set.

### 4.1 Core and identity

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_MODE` | — | *(empty → `local`)* | Selects the mode triad: `local`, `hybrid`, `server`. |
| `CORTEX_SERVER_NAME` | `server.name` | `cortex` | Server name. |
| `CORTEX_SERVER_VERSION` | `server.version` | `2.0.0` | Server version label. |
| `CORTEX_SERVER_TENANT_ID` | `server.tenant_id` | *(empty)* | Tenant UUID bound to the synthetic principal. Required in server mode. |
| `CORTEX_SERVER_WORKSPACE_ID` | `server.workspace_id` | *(empty)* | Default workspace UUID. Required in server mode. |
| `CORTEX_SERVER_PRINCIPAL_SUBJECT` | `server.principal_subject` | *(empty)* | Subject UUID of the synthetic constant principal. Required in server mode. |
| `CORTEX_SERVER_ROLES` | `server.roles` | `[]` | Comma-separated roles for the configured principal. |
| `CORTEX_SERVER_SCOPES` | `server.scopes` | `[]` | Comma-separated authorized scopes. |
| `CORTEX_SERVER_PROJECT_IDS` | `server.project_ids` | `[]` | Comma-separated project IDs. |
| `CORTEX_SERVER_CLASSIFICATION_CLEARANCE` | `server.classification_clearance` | `[]` | Comma-separated classification clearance labels. |
| `CORTEX_SERVER_GRANT_DIGEST` | `server.grant_digest` | `""` | Deprecated; calculated dynamically by PostgreSQL. |
| `CORTEX_SERVER_GRANT_VERSION` | `server.grant_version` | `0` | Deprecated; provisioned dynamically by PostgreSQL. |
| `CORTEX_SERVER_SECRETS_SIGNING_KEY` | `server.secrets.signing_key` | `""` | Server signing secret. Masked by `config show`. |
| `CORTEX_SERVER_SECRETS_OIDC_CLIENT_SECRET` | `server.secrets.oidc_client_secret` | `""` | OIDC client secret. Masked by `config show`. |

### 4.2 Server storage and startup

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_SERVER_STORAGE_DRIVER` | `server.storage.driver` | `postgres` | Persistence driver (`postgres` server / `sqlite` local). |
| `CORTEX_SERVER_STORAGE_DSN` | `server.storage.dsn` | *(empty)* | Non-privileged runtime PostgreSQL DSN. |
| `CORTEX_SERVER_STORAGE_MIGRATION_DSN` | `server.storage.migration_dsn` | *(empty)* | Privileged migration DSN. Runtime and migration roles must differ. |
| `CORTEX_SERVER_STORAGE_MAX_CONNS` | `server.storage.max_conns` | `10` | Maximum pool connections. |
| `CORTEX_SERVER_PROVIDER_EMBEDDING` | `server.provider.embedding` | `none` | Server embedding provider selection. |
| `CORTEX_SERVER_PROVIDER_VECTOR` | `server.provider.vector` | `none` | Server vector provider selection. |
| `CORTEX_SERVER_RAILWAY_INTERNAL_EMBEDDING_HOST` | `server.railway_internal_embedding_host` | `""` | Railway private hostname allowed for HTTP embeddings. |
| `CORTEX_SERVER_BOOTSTRAP_DEVELOPMENT` | `server.bootstrap_development` | `false` (opt-in; production-unsafe) | Development-only mode that reuses the runtime DSN for migrations when a dedicated migration role is not separated. The compose files ship it as the default source for containers; never enable it in a shared deployment. |
| `CORTEX_SERVER_AUTO_BOOTSTRAP` | *(compose/entrypoint only)* | `true` in compose | Enables the container entrypoint's automatic bootstrap path. |
| `CORTEX_SERVER_PRINT_BOOTSTRAP` | *(entrypoint only)* | `false` (opt-in; production-unsafe) | Gates echoing the bootstrap bearer into container logs. Leave unset in production; the bearer is otherwise written only to the state file. |
| `CORTEX_BOOTSTRAP_STATE_FILE` | *(entrypoint only)* | `/home/cortex/.cortex/server-bootstrap.env` | Persisted bootstrap state location in the container. |
| `CORTEX_SERVER_MULTI_TENANT` | *(retired key)* | `false` | Removed configuration key. Loading it is tolerated but it is ignored and dropped on the next save. |

### 4.3 HTTP, logging, and MCP

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_HTTP_ENABLED` | `http.enabled` | `true` | Enables `/api/*` and `/mcp`. |
| `CORTEX_HTTP_HOST` | `http.host` | `localhost` | Listen interface (`0.0.0.0` for containers). |
| `CORTEX_HTTP_PORT` | `http.port` | `7438` | Listen port. `CORTEX_PORT` is intentionally not honored. |
| `CORTEX_HTTP_TOKEN` | `http.token` | `""` | Static bearer for local HTTP/MCP and the server request plane. Required for non-loopback binds. |
| `CORTEX_HTTP_ALLOWED_ORIGINS` | `http.allowed_origins` | `[]` | Comma-separated CORS origins. |
| `CORTEX_LOGGING_LEVEL` | `logging.level` | `info` | `debug`, `info`, `warn`, `error`. |
| `CORTEX_LOGGING_FORMAT` | `logging.format` | `json` | `json`, `text`, `plain`. |
| `CORTEX_MCP_ENABLED` | `mcp.enabled` | `true` | Enables local MCP. |
| `CORTEX_MCP_PROFILE` | `mcp.profile` | `agent` | `agent`, `dev`, `minimal`. |
| `CORTEX_MCP_REMOTE_ENABLED` | `mcp.remote.enabled` | `false` | Proxy a remote MCP server. |
| `CORTEX_MCP_REMOTE_URL` | `mcp.remote.url` | `""` | Remote MCP destination. |
| `CORTEX_MCP_REMOTE_TOKEN_ENV` | `mcp.remote.token_env` | `CORTEX_REMOTE_TOKEN` | Name of the variable holding the remote bearer. |
| `CORTEX_MCP_REMOTE_TIMEOUT` | `mcp.remote.timeout` | `30s` | Remote MCP timeout. |
| `CORTEX_REMOTE_TOKEN` | *(token env target)* | *(empty)* | Default holder for remote MCP/sync bearer tokens. |

### 4.4 Database and storage

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_DATABASE_PATH` | `database.path` | `~/.cortex/cortex.db` | SQLite database path. |
| `CORTEX_DATABASE_IN_MEMORY` | `database.in_memory` | `false` | Use an in-memory database. |
| `CORTEX_DATABASE_PRAGMA_JOURNAL_MODE` | `database.pragma.journal_mode` | `WAL` | SQLite journal mode. |
| `CORTEX_DATABASE_PRAGMA_SYNCHRONOUS` | `database.pragma.synchronous` | `NORMAL` | SQLite synchronous mode. |
| `CORTEX_DATABASE_PRAGMA_CACHE_SIZE` | `database.pragma.cache_size` | `-64000` | SQLite cache size (KiB). |
| `CORTEX_DATABASE_PRAGMA_FOREIGN_KEYS` | `database.pragma.foreign_keys` | `true` | Enforce foreign keys. |
| `CORTEX_DATABASE_PRAGMA_TEMP_STORE` | `database.pragma.temp_store` | `MEMORY` | Temp store location. |
| `CORTEX_DATABASE_PRAGMA_MMAP_SIZE` | `database.pragma.mmap_size` | `268435456` | mmap size. |

### 4.5 Search, embeddings, and LLM

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_SEARCH_DEFAULT_LIMIT` | `search.default_limit` | `20` | Default result limit for store queries. |
| `CORTEX_SEARCH_MAX_LIMIT` | `search.max_limit` | `100` | Maximum result limit. |
| `CORTEX_SEARCH_FTS5` | `search.fts5` | `true` | Enable FTS5. |
| `CORTEX_SEARCH_VECTOR` | `search.vector` | `false` | Enable dense vector retrieval. |
| `CORTEX_SEARCH_FUSION_K` | `search.fusion_k` | `60` | RRF fusion constant. |
| `CORTEX_SEARCH_OLLAMA_AUTO_START` | `search.ollama_auto_start` | `false` | Auto-start Ollama when it is the embedding provider. |
| `CORTEX_SEARCH_EMBEDDING_PROVIDER` | `search.embedding_provider` | *(empty)* | Embedding provider (auto-mapped alias). |
| `CORTEX_SEARCH_EMBEDDING_MODEL` | `search.embedding_model` | *(empty)* | Embedding model (auto-mapped alias). |
| `CORTEX_SEARCH_EMBEDDING_BASE_URL` | `search.embedding_base_url` | *(empty)* | Embedding base URL (auto-mapped alias). |
| `CORTEX_EMBEDDING_PROVIDER` | `search.embedding_provider` | `none` | Embedding provider: `none`, `openai`, `ollama`, `openai-compatible`. Unknown values fail closed at load time. |
| `CORTEX_EMBEDDING_MODEL` | `search.embedding_model` | `text-embedding-3-small` | Embedding model identifier. |
| `CORTEX_EMBEDDING_BASE_URL` | `search.embedding_base_url` | `http://localhost:11434` (Ollama) | Embedding base URL. |
| `CORTEX_EMBEDDING_API_KEY` | *(credential)* | *(empty)* | Embedding credential. Required for hosted embedding providers. |
| `CORTEX_LLM_PROVIDER` | `ai.provider` | *(empty)* | LLM provider. |
| `CORTEX_LLM_MODEL` | `ai.model` | *(empty)* | LLM model. |
| `CORTEX_LLM_BASE_URL` | `ai.base_url` | *(empty)* | LLM base URL. |
| `CORTEX_LLM_API_KEY` | *(credential)* | *(empty)* | LLM credential. Vendor variables such as `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` are deliberately ignored. |
| `CORTEX_LLM_ALLOWED_HOSTS` | *(server LLM)* | *(empty)* | Comma-separated allowed LLM hosts. |
| `CORTEX_LLM_ALLOWED_PORTS` | *(server LLM)* | *(empty)* | Comma-separated allowed LLM ports. |
| `CORTEX_LLM_ALLOW_LOOPBACK` | *(server LLM)* | `false` | Allow loopback LLM destinations. |
| `CORTEX_LLM_ALLOW_LOOPBACK_HTTP` | *(server LLM)* | `false` | Allow plain HTTP loopback destinations. |
| `CORTEX_LLM_MAX_CONCURRENT` | *(server LLM)* | *(provider default)* | Maximum concurrent LLM requests. |
| `CORTEX_LLM_MAX_REDIRECTS` | *(server LLM)* | *(provider default)* | Maximum redirects. |
| `CORTEX_LLM_MAX_RESPONSE_BODY_BYTES` | *(server LLM)* | *(provider default)* | Maximum response body size. |
| `CORTEX_LLM_MAX_ERROR_BODY_BYTES` | *(server LLM)* | *(provider default)* | Maximum error body size. |
| `CORTEX_LLM_TIMEOUT` | *(server LLM)* | *(provider default)* | LLM request timeout (duration). |
| `CORTEX_LLM_CA_FILE` | *(server LLM)* | *(empty)* | PEM CA bundle for the LLM TLS handshake. |

### 4.6 Memory, lifecycle, and vectors

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_MEMORY_MAX_OBSERVATION_LENGTH` | `memory.max_observation_length` | `50000` | Maximum observation length. |
| `CORTEX_MEMORY_DEDUPE_WINDOW` | `memory.dedupe_window` | `15m` | Dedupe window. |
| `CORTEX_MEMORY_AUTO_ARCHIVE_DAYS` | `memory.auto_archive_days` | `90` | Auto-archive age. |
| `CORTEX_MEMORY_IMPORTANCE_DECAY_HALF_LIFE` | `memory.importance_decay_half_life` | `30` | Importance decay half-life (days). |
| `CORTEX_MEMORY_MIN_ARCHIVE_SCORE` | `memory.min_archive_score` | `0.1` | Minimum score to retain. |
| `CORTEX_LIFECYCLE_ENABLE_AUTO_ARCHIVE` | `lifecycle.enable_auto_archive` | `true` | Enable the auto-archive loop. |
| `CORTEX_LIFECYCLE_ARCHIVE_CHECK_INTERVAL` | `lifecycle.archive_check_interval` | `1h` | Archive check interval. |
| `CORTEX_VECTOR_PROVIDER` | `vector.provider` | *(empty → `sqlite_blob`)* | Vector backend: unset local BLOB, `qdrant`, or `pgvector`. |
| `CORTEX_VECTOR_PGVECTOR_DIMENSION` | *(server pgvector)* | resolved from model | pgvector dimension override. |
| `CORTEX_VECTOR_PGVECTOR_MAX_PARALLEL_WORKERS_PER_GATHER` | *(server pgvector tuning)* | *(unset → disabled)* | Enables `SET LOCAL max_parallel_workers_per_gather = N` inside pgvector exact-scan search transactions. A positive integer enables the tuning; unset, non-numeric, or `<= 0` leaves the default (no `SET LOCAL`). |
| `CORTEX_VECTOR_PGVECTOR_DISTANCE_MODE` | *(server pgvector tuning)* | `cosine` | Exact-scan distance operator: `cosine` (`<=>`, default) or `ip` (`<#>` normalized inner product, for pre-normalized corpora). Unrecognized values fall back to `cosine`. |
| `CORTEX_VECTOR_QDRANT_HOST` | `vector.qdrant.host` | `localhost` | Qdrant host (auto-mapped; adapter is server-only). |
| `CORTEX_VECTOR_QDRANT_PORT` | `vector.qdrant.port` | `6334` | Qdrant port (auto-mapped). |
| `CORTEX_VECTOR_QDRANT_COLLECTION` | `vector.qdrant.collection` | `cortex` | Qdrant collection (auto-mapped). |
| `CORTEX_VECTOR_QDRANT_API_KEY` | `vector.qdrant.api_key` | `""` | Qdrant API key (masked by `config show`). |
| `CORTEX_VECTOR_PGVECTOR_SCHEMA` | `vector.pgvector.schema` | `cortex_vector` | pgvector schema. |
| `CORTEX_VECTOR_PGVECTOR_TABLE` | `vector.pgvector.table` | `embeddings` | pgvector table. |
| `CORTEX_VECTOR_PGVECTOR_DSN` | `vector.pgvector.dsn` | *(empty)* | pgvector DSN (server-only). |

### 4.7 Sync

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `CORTEX_SYNC_ENABLED` | `sync.enabled` | `false` | Enable synchronization. |
| `CORTEX_SYNC_URL` | `sync.url` | `""` | Remote sync endpoint. |
| `CORTEX_SYNC_TOKEN_ENV` | `sync.token_env` | `CORTEX_REMOTE_TOKEN` | Name of the variable holding the sync bearer. |
| `CORTEX_SYNC_INTERVAL` | `sync.interval` | `30s` | Sync interval. |
| `CORTEX_SYNC_TIMEOUT` | `sync.timeout` | `30s` | Sync timeout. |

### 4.8 Ignored, legacy, and test-only variables

| Variable | Status | Meaning |
|---|---|---|
| `CORTEX_AI_API_KEY` | **Ignored** | Not accepted; use `CORTEX_LLM_API_KEY` or `CORTEX_EMBEDDING_API_KEY`. |
| `CORTEX_SEARCH_EMBEDDING_API_KEY` | **Ignored (legacy)** | Not accepted; use `CORTEX_EMBEDDING_API_KEY`. |
| `CORTEX_TEST_POSTGRES_DSN` | Test-only | Integration test runtime DSN (fails when missing, never skips). |
| `CORTEX_TEST_POSTGRES_MIGRATION_DSN` | Test-only | Integration test migration DSN. |
| `CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN` | Test-only | Integration test authz-admin DSN. |
| `CORTEX_QDRANT_HOST` / `CORTEX_QDRANT_PORT` | Test-only | Qdrant adapter integration test target. |
| `CORTEX_PGVECTOR_DSN` | Test-only | pgvector adapter integration test target. |
| `CORTEX_SPIKE_PG_ADMIN_DSN` / `CORTEX_SPIKE_PGBOUNCER_DSN` | Test-only | Governance spike DSNs. |
| `CORTEX_RESOURCE_HELPER_MARKER` | Test-only | Benchmark resource helper marker. |

---

## 5. Authentication matrix

Cortex has four credential planes. The table states which credential applies per
command and per transport.

| Command / transport | Local SQLite | Local HTTP (`serve`) | Server (`--mode server`) | MCP |
|---|---|---|---|---|
| `search`, `save`, `context`, `stats`, `timeline`, `revisions`, `export`, `import`, `merge-projects`, `gc`, `backup`, `reindex`, `ingest`, `code`, `watch`, `tui`, `doctor` (local), `status`, `migrate`, `config`, `setup` | **No auth** (zero-auth local) | n/a | n/a | n/a |
| `serve` | — | `/health` unauthenticated; `/api/*` and mounted MCP require `Authorization: Bearer <http.token>` when `http.token` is set. Without a token, only loopback binds are allowed. | — | — |
| `--mode server` | — | — | `/health` unauthenticated; every `/api/*` and `/mcp` request requires the configured static bearer (`http.token` / `CORTEX_HTTP_TOKEN`) before the synthetic constant principal is bound. | Static bearer |
| `mcp` (local stdio) | **No auth** (process-scoped) | — | — | stdio; no credential |
| `mcp` (remote proxy, `mcp.remote.enabled`) | — | — | — | Bearer read from `mcp.remote.token_env` (default `CORTEX_REMOTE_TOKEN`) |
| `sync --remote` | — | — | — | Bearer from `sync.token_env` (default `CORTEX_REMOTE_TOKEN`), falling back to `http.token` |
| `web key` (embedded web UI) | Web access key at `<config dir>/web.key` (independent of `http.token`) | same | same | — |
| `auth login` / `status` / `logout` | Manages `http.token` itself; no auth to run | — | — | — |

Notes:

- **Local zero-auth mode.** Local SQLite commands open the store directly and
  never require a credential.
- **Local `http.token`.** When set, local HTTP and mounted MCP require the bearer.
  A non-loopback `http.host` without `http.token` is refused at `serve` startup.
- **Server static bearer.** Single credential, verified on every authenticated
  request; tenant/workspace scope is configuration, not client input.
- **Embedded web.** The web access key is a distinct namespace; rotating it never
  changes `http.token`.
- **MCP profiles.** `agent`, `dev`, and `minimal` are the live catalogs. Profile
  selection does not change authentication.

---

## 6. Deprecated and retired surface

This appendix records retired or deprecated surface so migrating users find an
explicit notice and a supported replacement.

### 6.1 Retired MCP toolsets: `admin` and `temporal`

The `admin` (destructive deletion, project merging, compaction) and `temporal`
(execution duration, memory telemetry, manual RFC3339 timestamps) toolsets are
retired from standard agent discovery and must not be advertised by any live
command section.

- **Replacement:** administrative operations use the CLI (`cortex gc`,
  `cortex merge-projects`, `cortex doctor`), the TUI, and the web dashboard;
  telemetry and health use `/health`; temporal evolution is handled automatically
  through `topic_key` upserts and `cortex_revision_history`.
- The live `mcp` catalog is exactly `agent`, `dev`, and `minimal`.

### 6.2 Retired container image: `cortex-web`

The standalone `cortex-web` container image is retired.

- **Replacement:** the embedded web UI is mounted directly by `cortex serve`
  (local) and `cortex --mode server` (server), and its access key is managed with
  [`cortex web key`](#326-web).

### 6.3 Retired tool namespace: `mem_*` and Engram-era framing

The `mem_*` tool namespace and Engram-era compatibility framing are retired.

- **Replacement:** the supported namespace is `cortex_*`. Local catalog IDs and
  the Engram-era surface are not interchangeable; see [MCP.md](MCP.md).

### 6.4 Retired configuration key: `multi_tenant`

The `server.multi_tenant` configuration key is removed. The self-hosted server is
single-tenant: tenant and workspace are configuration constants.

- **Replacement:** `server.tenant_id` and `server.workspace_id`. A file that
  still carries `multi_tenant` loads without error (the key is unknown and dropped
  on the next save), but it has no effect.

### 6.5 Retired v1 root migrations

The root `migrations/001-014` files are retired v1 history and do not drive
startup.

- **Replacement:** the embedded, forward-only `migrations/v2/001_init.sql`
  baseline (SQLite) and `migrations/v2/100_server.sql` (PostgreSQL). Existing v1
  or foreign databases are refused without mutation; there is no automatic v1
  upgrade. Use [`cortex backup`](#320-backup) before any migration.
