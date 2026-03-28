# Cortex CLI Parity Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Cortex from a stub binary into a real memory application surface with working CLI commands, MCP startup, setup helpers, and Engram import.

**Architecture:** Add a thin command router in `cmd/cortex`, a shared bootstrap package in `internal/app`, and focused command packages in `internal/cli`, then port only the Engram behaviors that Cortex can truthfully support today.

**Tech Stack:** Go, SQLite (`modernc.org/sqlite`), existing Cortex stores, `mcp-go`, local Engram reference code at `D:\Fuentes\engram`

---

### Task 1: Bootstrap Application Runtime

**Files:**
- Create: `D:\Fuentes\cortex\internal\app\app.go`
- Create: `D:\Fuentes\cortex\internal\app\app_test.go`
- Modify: `D:\Fuentes\cortex\internal\config\config.go`
- Test: `D:\Fuentes\cortex\internal\app\app_test.go`

- [ ] **Step 1: Write the failing bootstrap tests**
- [ ] **Step 2: Run bootstrap tests and verify they fail**
- [ ] **Step 3: Implement config load, DB open, migrations, and store wiring**
- [ ] **Step 4: Run bootstrap tests and targeted existing config/database/migration tests**
- [ ] **Step 5: Verify created app object exposes cleanup and store access cleanly**

### Task 2: Build Command Router

**Files:**
- Modify: `D:\Fuentes\cortex\cmd\cortex\main.go`
- Create: `D:\Fuentes\cortex\cmd\cortex\main_test.go`
- Create: `D:\Fuentes\cortex\internal\cli\help.go`
- Test: `D:\Fuentes\cortex\cmd\cortex\main_test.go`

- [ ] **Step 1: Write failing command-dispatch tests for known and unknown commands**
- [ ] **Step 2: Run dispatch tests and verify they fail**
- [ ] **Step 3: Implement top-level command parsing and usage output**
- [ ] **Step 4: Run dispatch tests and confirm pass**
- [ ] **Step 5: Smoke-test `go run ./cmd/cortex --help`**

### Task 3: Implement Core Memory CLI Commands

**Files:**
- Create: `D:\Fuentes\cortex\internal\cli\memory.go`
- Create: `D:\Fuentes\cortex\internal\cli\memory_test.go`
- Modify: `D:\Fuentes\cortex\internal\store\search\store.go`
- Test: `D:\Fuentes\cortex\internal\cli\memory_test.go`

- [ ] **Step 1: Write failing tests for `search`, `save`, `context`, `stats`, and `timeline` command flows**
- [ ] **Step 2: Run those tests and verify they fail**
- [ ] **Step 3: Implement command handlers using Cortex stores**
- [ ] **Step 4: Align output format with Engram where Cortex support exists**
- [ ] **Step 5: Run command tests and relevant store tests**

### Task 4: Implement MCP Command

**Files:**
- Create: `D:\Fuentes\cortex\internal\cli\mcp.go`
- Create: `D:\Fuentes\cortex\internal\cli\mcp_test.go`
- Modify: `D:\Fuentes\cortex\internal\mcp\server.go`
- Test: `D:\Fuentes\cortex\internal\cli\mcp_test.go`

- [ ] **Step 1: Write failing tests for `cortex mcp` startup and `--tools` handling**
- [ ] **Step 2: Run tests and verify they fail**
- [ ] **Step 3: Implement MCP command bootstrap through `internal/app`**
- [ ] **Step 4: Ensure tool profile behavior matches current Cortex support**
- [ ] **Step 5: Run MCP tests and `go test ./internal/mcp`**

### Task 5: Implement Setup Support

**Files:**
- Create: `D:\Fuentes\cortex\internal\setup\setup.go`
- Create: `D:\Fuentes\cortex\internal\setup\setup_test.go`
- Create: `D:\Fuentes\cortex\internal\cli\setup.go`
- Test: `D:\Fuentes\cortex\internal\setup\setup_test.go`

- [ ] **Step 1: Write failing tests for supported agents and generated Cortex config**
- [ ] **Step 2: Run setup tests and verify they fail**
- [ ] **Step 3: Port the minimal Engram setup logic, renaming all binary/config references to Cortex**
- [ ] **Step 4: Implement CLI wrapper for `cortex setup`**
- [ ] **Step 5: Run setup tests**

### Task 6: Implement Engram Import

**Files:**
- Create: `D:\Fuentes\cortex\internal\migration\engram_import.go`
- Create: `D:\Fuentes\cortex\internal\migration\engram_import_test.go`
- Create: `D:\Fuentes\cortex\internal\cli\import.go`
- Test: `D:\Fuentes\cortex\internal\migration\engram_import_test.go`

- [ ] **Step 1: Write failing tests using Engram fixtures or a temporary Engram DB**
- [ ] **Step 2: Run import tests and verify they fail**
- [ ] **Step 3: Implement import of sessions, observations, and prompts into Cortex**
- [ ] **Step 4: Add `--from-engram` flow in CLI with source-path support**
- [ ] **Step 5: Run import tests and verify imported data is queryable in Cortex**

### Task 7: Handle Serve Honestly

**Files:**
- Create: `D:\Fuentes\cortex\internal\cli\serve.go`
- Create: `D:\Fuentes\cortex\internal\cli\serve_test.go`
- Test: `D:\Fuentes\cortex\internal\cli\serve_test.go`

- [ ] **Step 1: Write failing test for current expected `serve` behavior**
- [ ] **Step 2: Decide implementation path based on actual HTTP support in repo**
- [ ] **Step 3: If HTTP is real, wire it through `internal/app`; otherwise return explicit not-implemented error**
- [ ] **Step 4: Run serve tests**
- [ ] **Step 5: Verify help text matches implemented behavior**

### Task 8: Reconcile Documentation

**Files:**
- Modify: `D:\Fuentes\cortex\README.md`
- Modify: `D:\Fuentes\cortex\CLAUDE.md`
- Test: manual verification of command examples against real binary

- [ ] **Step 1: Update README command list and examples to match actual implementation**
- [ ] **Step 2: Remove or defer unsupported claims in docs**
- [ ] **Step 3: Update developer docs that still describe stubs**
- [ ] **Step 4: Manually compare documented commands with `--help` output**

### Task 9: Final Verification

**Files:**
- Modify: any touched files above as needed for fixes

- [ ] **Step 1: Run `gofmt -w` on all changed Go files**
- [ ] **Step 2: Run targeted tests for new command/setup/import packages**
- [ ] **Step 3: Run `go test ./...`**
- [ ] **Step 4: Run `go build ./...`**
- [ ] **Step 5: Smoke-test `cortex mcp`, `cortex save`, and `cortex search` locally**
