#!/usr/bin/env bash

set -euo pipefail

# obolup.sh - Bootstrap installer for Obol Stack
# Usage: curl -sSL https://raw.githubusercontent.com/obol/obol-stack/main/obolup.sh | bash

# Color output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# XDG Base Directory specification
# https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html
XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
XDG_DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"

# Configuration directories with XDG defaults
OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$XDG_CONFIG_HOME/obol}"
OBOL_STATE_DIR="${OBOL_STATE_DIR:-$XDG_DATA_HOME/obol}"
OBOL_BIN_DIR="${OBOL_BIN_DIR:-$OBOL_CONFIG_DIR/bin}"

# Logging functions
log_info() {
	echo -e "${BLUE}==>${NC} $1"
}

log_success() {
	echo -e "${GREEN}✓${NC} $1"
}

log_warn() {
	echo -e "${YELLOW}!${NC} $1"
}

log_error() {
	echo -e "${RED}✗${NC} $1"
}

# Check if command exists
command_exists() {
	command -v "$1" >/dev/null 2>&1
}

# Validate prerequisites
validate_prerequisites() {
	log_info "Validating prerequisites..."

	# Check for Docker
	if ! command_exists docker; then
		log_error "Docker is not installed"
		echo ""
		echo "Please install Docker first:"
		echo "  - Linux: https://docs.docker.com/engine/install/"
		echo "  - macOS: https://docs.docker.com/desktop/install/mac-install/"
		echo "  - Windows: https://docs.docker.com/desktop/install/windows-install/"
		exit 1
	fi

	# Check if Docker daemon is running
	if ! docker info >/dev/null 2>&1; then
		log_error "Docker daemon is not running"
		echo ""
		echo "Please start Docker daemon:"
		echo "  - systemctl start docker    (Linux with systemd)"
		echo "  - Open Docker Desktop       (macOS/Windows)"
		exit 1
	fi

	log_success "Docker installed and running"
}

# Create directory structure
create_directories() {
	log_info "Creating directory structure..."

	mkdir -p "$OBOL_BIN_DIR"
	mkdir -p "$OBOL_CONFIG_DIR/cluster/k3d"
	mkdir -p "$OBOL_CONFIG_DIR/cluster/kubeconfig"
	mkdir -p "$OBOL_CONFIG_DIR/helmfile"
	mkdir -p "$OBOL_STATE_DIR/volumes"
	mkdir -p "$OBOL_STATE_DIR/backups"

	log_success "Directories created"
}

# Install obol binary
install_obol_binary() {
	log_info "Installing obol binary..."

	# Check if obol binary already exists
	if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		local current_version
		current_version=$("$OBOL_BIN_DIR/obol" --version 2>/dev/null | grep -oP 'version \K[0-9.]+' || echo "unknown")
		log_info "Found existing obol binary (version: $current_version)"
		log_info "Upgrading..."
	fi

	# For development, we'll build from source if go is available
	if command_exists go && [[ -f "cmd/obol/main.go" ]]; then
		log_info "Building from source..."
		go build -o "$OBOL_BIN_DIR/obol" ./cmd/obol
		chmod +x "$OBOL_BIN_DIR/obol"

		local new_version
		new_version=$("$OBOL_BIN_DIR/obol" --version 2>/dev/null | grep -oP 'version \K[0-9.]+' || echo "unknown")
		log_success "Installed obol binary (version: $new_version)"
	else
		# In production, this would download from GitHub releases
		log_warn "Production binary download not yet implemented"
		log_info "Please build manually: go build -o $OBOL_BIN_DIR/obol ./cmd/obol"
		return 1
	fi
}

# Download dependencies (stub for now)
install_dependencies() {
	log_info "Checking dependencies..."

	local deps=("k3d" "kubectl" "helm" "helmfile" "k9s")

	for dep in "${deps[@]}"; do
		if command_exists "$dep"; then
			log_success "$dep already installed"
		else
			log_warn "$dep not found (dependency installation not yet implemented)"
		fi
	done
}

# Print post-install instructions
print_instructions() {
	echo ""
	log_success "Obol Stack installation complete!"
	echo ""
	echo "Add the following to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
	echo ""
	echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
	echo "Then reload your shell or run:"
	echo ""
	echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
	echo "Verify installation:"
	echo ""
	echo "  obol version"
	echo ""
	echo "To initialize a cluster, run:"
	echo ""
	echo "  obol cluster init"
	echo "  obol cluster up"
	echo "  obol cluster connect"
	echo ""
}

# Main installation flow
main() {
	echo ""
	echo "╔═══════════════════════════════════════════╗"
	echo "║                                           ║"
	echo "║     Obol Stack Bootstrap Installer        ║"
	echo "║                                           ║"
	echo "╚═══════════════════════════════════════════╝"
	echo ""

	validate_prerequisites
	create_directories
	install_obol_binary
	install_dependencies
	print_instructions

	echo ""
	log_success "Setup complete!"
}

# Run main
main
