#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# migrate-from-engram.sh — Migrate from Engram to Cortex
#
# What this script does:
#   1. Checks prerequisites (cortex installed, engram database exists)
#   2. Imports all Engram data (sessions, observations, prompts) into Cortex
#   3. Reconfigures AI agents to use Cortex instead of Engram
#   4. Optionally uninstalls Engram (Homebrew, go binary, or manual)
#   5. Optionally removes Engram data
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/lleontor705/cortex/master/scripts/migrate-from-engram.sh | bash
#   # or
#   ./scripts/migrate-from-engram.sh
#   # or with options
#   ./scripts/migrate-from-engram.sh --no-uninstall --engram-db /custom/path/engram.db
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

# ─── Colors ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

info()    { printf "${BLUE}[INFO]${NC}  %s\n" "$1"; }
success() { printf "${GREEN}[OK]${NC}    %s\n" "$1"; }
warn()    { printf "${YELLOW}[WARN]${NC}  %s\n" "$1"; }
error()   { printf "${RED}[ERROR]${NC} %s\n" "$1"; }
step()    { printf "\n${BOLD}${CYAN}── %s${NC}\n" "$1"; }

# ─── Parse Arguments ─────────────────────────────────────────────────────────

ENGRAM_DB=""
NO_UNINSTALL=false
NO_PROMPT=false
KEEP_DATA=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --engram-db)    ENGRAM_DB="$2"; shift 2 ;;
    --no-uninstall) NO_UNINSTALL=true; shift ;;
    --no-prompt)    NO_PROMPT=true; shift ;;
    --keep-data)    KEEP_DATA=true; shift ;;
    -h|--help)
      echo "Usage: migrate-from-engram.sh [OPTIONS]"
      echo ""
      echo "Options:"
      echo "  --engram-db PATH   Path to Engram database (default: auto-detect)"
      echo "  --no-uninstall     Skip Engram uninstallation"
      echo "  --keep-data        Keep Engram data directory after migration"
      echo "  --no-prompt        Skip confirmation prompts"
      echo "  -h, --help         Show this help"
      exit 0
      ;;
    *) error "Unknown option: $1"; exit 1 ;;
  esac
done

# ─── Banner ──────────────────────────────────────────────────────────────────

printf "\n${BOLD}${CYAN}"
echo "  ╔═══════════════════════════════════════════════════════════╗"
echo "  ║              Engram → Cortex Migration                    ║"
echo "  ║                                                           ║"
echo "  ║  Imports your memories, reconfigures agents,              ║"
echo "  ║  and optionally uninstalls Engram.                        ║"
echo "  ╚═══════════════════════════════════════════════════════════╝"
printf "${NC}\n"

# ─── Step 1: Check Prerequisites ─────────────────────────────────────────────

step "Step 1/5: Checking prerequisites"

# Check cortex is installed
if ! command -v cortex &> /dev/null; then
  error "cortex is not installed."
  echo ""
  echo "  Install with:"
  echo "    brew install lleontor705/tap/cortex"
  echo "    # or"
  echo "    go install github.com/lleontor705/cortex/cmd/cortex@latest"
  echo ""
  echo "  More options: https://github.com/lleontor705/cortex/blob/master/docs/INSTALLATION.md"
  exit 1
fi
success "cortex found: $(which cortex)"

# Auto-detect Engram database
if [ -z "$ENGRAM_DB" ]; then
  if [ -f "$HOME/.engram/engram.db" ]; then
    ENGRAM_DB="$HOME/.engram/engram.db"
  elif [ -n "${USERPROFILE:-}" ] && [ -f "$USERPROFILE/.engram/engram.db" ]; then
    ENGRAM_DB="$USERPROFILE/.engram/engram.db"
  elif [ -n "${APPDATA:-}" ] && [ -f "$APPDATA/engram/engram.db" ]; then
    ENGRAM_DB="$APPDATA/engram/engram.db"
  fi
fi

if [ -z "$ENGRAM_DB" ] || [ ! -f "$ENGRAM_DB" ]; then
  error "Engram database not found."
  echo "  Looked in: ~/.engram/engram.db"
  echo "  Specify manually: --engram-db /path/to/engram.db"
  exit 1
fi

# Get DB size
DB_SIZE=$(du -h "$ENGRAM_DB" | cut -f1)
success "Engram database found: $ENGRAM_DB ($DB_SIZE)"

# Count records
if command -v sqlite3 &> /dev/null; then
  OBS_COUNT=$(sqlite3 "$ENGRAM_DB" "SELECT COUNT(*) FROM observations WHERE deleted_at IS NULL;" 2>/dev/null || echo "?")
  SESS_COUNT=$(sqlite3 "$ENGRAM_DB" "SELECT COUNT(*) FROM sessions;" 2>/dev/null || echo "?")
  info "Found $OBS_COUNT observations and $SESS_COUNT sessions"
fi

# ─── Step 2: Confirm ─────────────────────────────────────────────────────────

if [ "$NO_PROMPT" = false ]; then
  step "Confirm migration"
  echo ""
  echo "  This will:"
  echo "    1. Import all Engram data into Cortex"
  echo "    2. Reconfigure your AI agents to use Cortex"
  if [ "$NO_UNINSTALL" = false ]; then
    echo "    3. Uninstall Engram"
  fi
  if [ "$KEEP_DATA" = false ]; then
    echo "    4. Remove Engram data directory"
  fi
  echo ""
  read -p "  Continue? [y/N] " -n 1 -r
  echo ""
  if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    info "Migration cancelled."
    exit 0
  fi
fi

# ─── Step 3: Import Data ─────────────────────────────────────────────────────

step "Step 2/5: Importing Engram data into Cortex"

if cortex import --from-engram --path "$ENGRAM_DB" 2>&1; then
  success "Data imported successfully"
else
  error "Import failed. Your Engram data is untouched."
  exit 1
fi

# Verify import
info "Verifying import..."
cortex stats 2>/dev/null || true

# ─── Step 4: Reconfigure Agents ──────────────────────────────────────────────

step "Step 3/5: Reconfiguring AI agents"

# Claude Code
if command -v claude &> /dev/null; then
  info "Reconfiguring Claude Code..."
  # Remove Engram plugin if installed
  claude plugin uninstall engram 2>/dev/null || true
  # Remove Engram MCP config
  rm -f "$HOME/.claude/mcp/engram.json" 2>/dev/null || true
  # Install Cortex
  cortex setup claude-code 2>/dev/null && success "Claude Code: done" || warn "Claude Code: manual setup needed"
else
  info "Claude Code CLI not found, skipping"
fi

# OpenCode
OPENCODE_DIR="$HOME/.config/opencode"
if [ -d "$OPENCODE_DIR" ]; then
  info "Reconfiguring OpenCode..."
  rm -f "$OPENCODE_DIR/plugins/engram.ts" 2>/dev/null || true
  rm -f "$OPENCODE_DIR/engram-mcp.json" 2>/dev/null || true
  cortex setup opencode 2>/dev/null && success "OpenCode: done" || warn "OpenCode: manual setup needed"
else
  info "OpenCode config not found, skipping"
fi

# Gemini CLI
GEMINI_DIR="$HOME/.gemini"
if [ -d "$GEMINI_DIR" ]; then
  info "Reconfiguring Gemini CLI..."
  # Remove engram from settings.json if present
  if [ -f "$GEMINI_DIR/settings.json" ]; then
    # Simple approach: just overwrite with cortex config
    cortex setup gemini-cli 2>/dev/null && success "Gemini CLI: done" || warn "Gemini CLI: manual setup needed"
  fi
else
  info "Gemini CLI config not found, skipping"
fi

# Codex
CODEX_DIR="$HOME/.codex"
if [ -d "$CODEX_DIR" ]; then
  info "Reconfiguring Codex..."
  rm -f "$CODEX_DIR/engram-instructions.md" 2>/dev/null || true
  rm -f "$CODEX_DIR/engram-compact-prompt.md" 2>/dev/null || true
  cortex setup codex 2>/dev/null && success "Codex: done" || warn "Codex: manual setup needed"
else
  info "Codex config not found, skipping"
fi

# VS Code
VSCODE_MCP="$HOME/.vscode/mcp.json"
if [ -f "$VSCODE_MCP" ] && grep -q "engram" "$VSCODE_MCP" 2>/dev/null; then
  info "VS Code: found engram in mcp.json"
  warn "Please manually update .vscode/mcp.json: replace 'engram' with 'cortex'"
fi

success "Agent reconfiguration complete"

# ─── Step 5: Uninstall Engram ────────────────────────────────────────────────

if [ "$NO_UNINSTALL" = false ]; then
  step "Step 4/5: Uninstalling Engram"

  # Stop engram server if running
  pkill -f "engram serve" 2>/dev/null || true

  # Homebrew
  if command -v brew &> /dev/null && brew list engram &> /dev/null; then
    info "Uninstalling via Homebrew..."
    brew uninstall engram 2>/dev/null && success "Homebrew: uninstalled" || warn "Homebrew uninstall failed"
    brew untap gentleman-programming/tap 2>/dev/null || true
  # go binary
  elif command -v engram &> /dev/null; then
    ENGRAM_BIN=$(which engram)
    info "Removing binary: $ENGRAM_BIN"
    rm -f "$ENGRAM_BIN" 2>/dev/null && success "Binary removed" || warn "Could not remove $ENGRAM_BIN (try with sudo)"
  else
    info "Engram binary not found, skipping"
  fi
else
  info "Skipping Engram uninstallation (--no-uninstall)"
fi

# ─── Step 6: Clean Up Data ──────────────────────────────────────────────────

if [ "$KEEP_DATA" = false ]; then
  step "Step 5/5: Cleaning up Engram data"

  ENGRAM_DATA_DIR="$(dirname "$ENGRAM_DB")"

  if [ "$NO_PROMPT" = false ]; then
    read -p "  Remove Engram data directory ($ENGRAM_DATA_DIR)? [y/N] " -n 1 -r
    echo ""
    if [[ $REPLY =~ ^[Yy]$ ]]; then
      rm -rf "$ENGRAM_DATA_DIR" 2>/dev/null && success "Engram data removed" || warn "Could not remove $ENGRAM_DATA_DIR"
    else
      info "Keeping Engram data"
    fi
  else
    info "Keeping Engram data (use manual cleanup: rm -rf $ENGRAM_DATA_DIR)"
  fi
else
  info "Keeping Engram data (--keep-data)"
fi

# ─── Done ────────────────────────────────────────────────────────────────────

printf "\n${BOLD}${GREEN}"
echo "  ╔═══════════════════════════════════════════════════════════╗"
echo "  ║              Migration Complete!                          ║"
echo "  ╚═══════════════════════════════════════════════════════════╝"
printf "${NC}\n"

echo "  Your memories are now in Cortex with full knowledge graph,"
echo "  importance scoring, and entity linking capabilities."
echo ""
echo "  Next steps:"
echo "    cortex search \"your query\"     Search your imported memories"
echo "    cortex stats                    Check memory statistics"
echo "    cortex tui                      Browse in terminal UI"
echo ""
echo "  Docs: https://github.com/lleontor705/cortex"
echo ""
