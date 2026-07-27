#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# install.sh — Install Cortex
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/lleontor705/cortex/master/scripts/install.sh | bash
#   # or with a specific version
#   curl -sSL https://raw.githubusercontent.com/lleontor705/cortex/master/scripts/install.sh | bash -s -- v0.1.0
# ─────────────────────────────────────────────────────────────────────────────

set -euo pipefail

REPO="lleontor705/cortex"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
VERSION="${1:-latest}"

# ─── Colors ──────────────────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()    { printf "${CYAN}[INFO]${NC}  %s\n" "$1"; }
success() { printf "${GREEN}[OK]${NC}    %s\n" "$1"; }
error()   { printf "${RED}[ERROR]${NC} %s\n" "$1" >&2; exit 1; }

# ─── Banner ──────────────────────────────────────────────────────────────────

printf "\n${BOLD}${CYAN}"
echo "  ╔═══════════════════════════════════════════════════════════╗"
echo "  ║              Cortex Installer                             ║"
echo "  ║  Persistent memory for AI coding agents                   ║"
echo "  ╚═══════════════════════════════════════════════════════════╝"
printf "${NC}\n"

# ─── Detect Platform ─────────────────────────────────────────────────────────

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  linux)  OS="linux" ;;
  darwin) OS="darwin" ;;
  *)      error "Unsupported OS: $OS. Use 'go install' instead." ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)             error "Unsupported architecture: $ARCH" ;;
esac

info "Detected platform: ${OS}/${ARCH}"

# ─── Get Version ─────────────────────────────────────────────────────────────

if [ "$VERSION" = "latest" ]; then
  VERSION=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')
  if [ -z "$VERSION" ]; then
    error "Could not determine latest version. Specify version: $0 v0.1.0"
  fi
fi

info "Installing cortex ${VERSION}"

# ─── Download ────────────────────────────────────────────────────────────────

ARCHIVE="cortex_${VERSION#v}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"

TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

info "Downloading ${URL}..."
if ! curl -sSfL "$URL" -o "${TMPDIR}/${ARCHIVE}"; then
  error "Download failed. Check version and platform at https://github.com/${REPO}/releases"
fi

# ─── Extract & Install ───────────────────────────────────────────────────────

info "Extracting..."
tar -xzf "${TMPDIR}/${ARCHIVE}" -C "$TMPDIR"

if [ ! -f "${TMPDIR}/cortex" ]; then
  error "Binary not found in archive"
fi

chmod +x "${TMPDIR}/cortex"

info "Installing to ${INSTALL_DIR}/cortex..."
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMPDIR}/cortex" "${INSTALL_DIR}/cortex"
else
  sudo mv "${TMPDIR}/cortex" "${INSTALL_DIR}/cortex"
fi

# ─── Verify ──────────────────────────────────────────────────────────────────

if command -v cortex &> /dev/null; then
  success "cortex installed: $(cortex version 2>/dev/null || echo "$VERSION")"
else
  warn "cortex installed to ${INSTALL_DIR}/cortex but not in PATH"
  echo "  Add to PATH: export PATH=\"${INSTALL_DIR}:\$PATH\""
fi

# ─── Next Steps ──────────────────────────────────────────────────────────────

printf "\n${BOLD}${GREEN}Installation complete!${NC}\n\n"
echo "  Setup your agent:"
echo "    cortex setup claude-code"
echo "    cortex setup opencode"
echo "    cortex setup gemini-cli"
echo "    cortex setup codex"
echo ""
echo "  Docs: https://github.com/${REPO}"
echo ""
