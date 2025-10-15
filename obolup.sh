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

# Development mode detection
if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
	# Get script directory for development mode
	SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	WORKSPACE_DIR="$SCRIPT_DIR/.workspace"

	# Override directories to use local .workspace
	OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$WORKSPACE_DIR/config}"
	OBOL_DATA_DIR="${OBOL_DATA_DIR:-$WORKSPACE_DIR/data}"
	OBOL_STATE_DIR="${OBOL_STATE_DIR:-$WORKSPACE_DIR/state}"
	OBOL_BIN_DIR="${OBOL_BIN_DIR:-$WORKSPACE_DIR/bin}"

	log_warn() { echo -e "${YELLOW}!${NC} $1"; }
	log_warn "Development mode enabled - using local .workspace directory"
else
	# XDG Base Directory specification
	# https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html
	XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
	XDG_DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
	XDG_STATE_HOME="${XDG_STATE_HOME:-$HOME/.local/state}"

	# Configuration directories with XDG defaults
	OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$XDG_CONFIG_HOME/obol}"
	OBOL_DATA_DIR="${OBOL_DATA_DIR:-$XDG_DATA_HOME/obol}"
	OBOL_STATE_DIR="${OBOL_STATE_DIR:-$XDG_STATE_HOME/obol}"
	OBOL_BIN_DIR="${OBOL_BIN_DIR:-$OBOL_CONFIG_DIR/bin}"
fi

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

# Create directory structure
create_directories() {
	log_info "Creating directory structure..."

	mkdir -p "$OBOL_BIN_DIR"
	mkdir -p "$OBOL_CONFIG_DIR"
	mkdir -p "$OBOL_DATA_DIR"
	mkdir -p "$OBOL_STATE_DIR"

	log_success "Directories created"
}

# Get version information
get_version_info() {
	local version="0.0.0" # Default semantic version
	local git_commit="unknown"
	local build_time
	local git_dirty="false"

	# Get build timestamp (YYYYMMDDHHMMSS format)
	build_time=$(date -u +"%Y%m%d%H%M%S")

	# Get git information if available
	if command_exists git && [[ -d .git ]]; then
		# Get short commit hash
		git_commit=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

		# Check if repo is dirty (has uncommitted changes)
		if ! git diff --quiet 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
			git_dirty="true"
		fi

		# Try to get version from git tag
		local git_tag
		git_tag=$(git describe --tags --exact-match 2>/dev/null || echo "")
		if [[ -n "$git_tag" ]]; then
			# Strip 'v' prefix if present
			version="${git_tag#v}"
		fi
	fi

	echo "$version" "$git_commit" "$build_time" "$git_dirty"
}

# Install obol binary for development mode (wrapper script)
install_dev_wrapper() {
	log_info "Installing development wrapper script..."

	# Get script directory
	SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

	# Create wrapper script that uses 'go run'
	cat >"$OBOL_BIN_DIR/obol" <<'EOF'
#!/usr/bin/env bash
# Obol CLI Development Wrapper
# This script runs the obol CLI using 'go run' for rapid development

# Find the project root (where obolup.sh is located)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Run the CLI
cd "$SCRIPT_DIR" && exec go run ./cmd/obol "$@"
EOF

	chmod +x "$OBOL_BIN_DIR/obol"
	log_success "Installed development wrapper at $OBOL_BIN_DIR/obol"
}

# Download and install binary from GitHub releases
download_release() {
	local release_tag="$1"
	log_info "Downloading release: $release_tag"

	# Detect OS and architecture
	local os arch
	case "$(uname -s)" in
	Linux*) os="linux" ;;
	Darwin*) os="darwin" ;;
	*)
		log_error "Unsupported OS: $(uname -s)"
		return 1
		;;
	esac

	case "$(uname -m)" in
	x86_64) arch="amd64" ;;
	aarch64 | arm64) arch="arm64" ;;
	*)
		log_error "Unsupported architecture: $(uname -m)"
		return 1
		;;
	esac

	# Construct download URL
	local binary_name="obol_${os}_${arch}"
	local download_url="https://github.com/obol/obol-stack/releases/download/${release_tag}/${binary_name}"

	log_info "Downloading from: $download_url"

	# Download binary
	if command_exists curl; then
		if ! curl -fsSL "$download_url" -o "$OBOL_BIN_DIR/obol"; then
			log_warn "Failed to download release $release_tag"
			return 1
		fi
	elif command_exists wget; then
		if ! wget -q "$download_url" -O "$OBOL_BIN_DIR/obol"; then
			log_warn "Failed to download release $release_tag"
			return 1
		fi
	else
		log_error "Neither curl nor wget is available"
		return 1
	fi

	chmod +x "$OBOL_BIN_DIR/obol"
	log_success "Downloaded and installed release $release_tag"
	return 0
}

# Build from source (latest or specific commit)
build_from_source() {
	local build_ref="${1:-main}"
	log_info "Building from source (ref: $build_ref)..."

	# Create temporary directory
	local tmp_dir
	tmp_dir=$(mktemp -d)
	trap "rm -rf '$tmp_dir'" EXIT

	log_info "Cloning repository..."
	if ! git clone --depth 1 --branch "$build_ref" https://github.com/obol/obol-stack.git "$tmp_dir" 2>/dev/null; then
		# If branch doesn't exist, try as a tag
		if ! git clone https://github.com/obol/obol-stack.git "$tmp_dir" 2>/dev/null; then
			log_error "Failed to clone repository"
			return 1
		fi
		cd "$tmp_dir"
		git checkout "$build_ref" 2>/dev/null || {
			log_error "Failed to checkout ref: $build_ref"
			return 1
		}
	fi

	cd "$tmp_dir"

	# Get version information
	read -r version git_commit build_time git_dirty <<<"$(get_version_info)"

	# Build binary
	log_info "Building binary..."
	local ldflags="-X github.com/obol/obol-stack/internal/version.Version=$version"
	ldflags="$ldflags -X github.com/obol/obol-stack/internal/version.GitCommit=$git_commit"
	ldflags="$ldflags -X github.com/obol/obol-stack/internal/version.BuildTime=$build_time"
	ldflags="$ldflags -X github.com/obol/obol-stack/internal/version.GitDirty=$git_dirty"

	if ! go build -ldflags "$ldflags" -o "$OBOL_BIN_DIR/obol" ./cmd/obol; then
		log_error "Failed to build binary"
		return 1
	fi

	chmod +x "$OBOL_BIN_DIR/obol"
	log_success "Built and installed from source"
	return 0
}

# Install obol binary
install_obol_binary() {
	log_info "Installing obol binary..."

	# Development mode: install wrapper script
	if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
		install_dev_wrapper
		return 0
	fi

	# Production mode: handle OBOL_RELEASE
	local release="${OBOL_RELEASE:-latest}"

	if [[ "$release" == "latest" ]]; then
		log_info "OBOL_RELEASE=latest: attempting to download latest release..."

		# Try to get latest release tag from GitHub API
		local latest_tag
		if command_exists curl; then
			latest_tag=$(curl -fsSL https://api.github.com/repos/obol/obol-stack/releases/latest 2>/dev/null | grep -oP '"tag_name": "\K(.*)(?=")')
		fi

		# If we got a tag, try to download it
		if [[ -n "$latest_tag" ]]; then
			if download_release "$latest_tag"; then
				return 0
			fi
			log_warn "Download failed, falling back to building from source..."
		else
			log_info "No releases found, building from source..."
		fi

		# Fallback: build from source
		build_from_source "main"
	else
		# Specific release requested
		log_info "Attempting to download release: $release"
		if download_release "$release"; then
			return 0
		fi

		log_warn "Release $release not found, building from source..."
		build_from_source "$release"
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

	create_directories
	install_obol_binary
	install_dependencies
	print_instructions

	echo ""
	log_success "Setup complete!"
}

# Run main
main
