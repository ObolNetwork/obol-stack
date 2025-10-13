#!/usr/bin/env bash

set -euo pipefail

# obolup.sh - Bootstrap installer for Obol Stack
# Usage: curl -sSL https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/obolup.sh | bash

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
	XDG_BIN_HOME="${XDG_BIN_HOME:-$HOME/.local/bin}"

	# Configuration directories with XDG defaults
	OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$XDG_CONFIG_HOME/obol}"
	OBOL_DATA_DIR="${OBOL_DATA_DIR:-$XDG_DATA_HOME/obol}"
	OBOL_STATE_DIR="${OBOL_STATE_DIR:-$XDG_STATE_HOME/obol}"
	OBOL_BIN_DIR="${OBOL_BIN_DIR:-$XDG_BIN_HOME}"
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
	local download_url="https://github.com/ObolNetwork/obol-stack/releases/download/${release_tag}/${binary_name}"

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
	if ! git clone --depth 1 --branch "$build_ref" https://github.com/ObolNetwork/obol-stack.git "$tmp_dir" 2>/dev/null; then
		# If branch doesn't exist, try as a tag
		if ! git clone https://github.com/ObolNetwork/obol-stack.git "$tmp_dir" 2>/dev/null; then
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
	local ldflags="-X github.com/ObolNetwork/obol-stack/internal/version.Version=$version"
	ldflags="$ldflags -X github.com/ObolNetwork/obol-stack/internal/version.GitCommit=$git_commit"
	ldflags="$ldflags -X github.com/ObolNetwork/obol-stack/internal/version.BuildTime=$build_time"
	ldflags="$ldflags -X github.com/ObolNetwork/obol-stack/internal/version.GitDirty=$git_dirty"

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
			latest_tag=$(curl -fsSL https://api.github.com/repos/ObolNetwork/obol-stack/releases/latest 2>/dev/null | grep -oP '"tag_name": "\K(.*)(?=")')
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

# Detect platform
detect_platform() {
	local platform
	case "$(uname -s)" in
	Linux*)
		# Check if running under WSL
		if grep -qi microsoft /proc/version 2>/dev/null; then
			platform="linux" # WSL uses Linux binaries
		else
			platform="linux"
		fi
		;;
	Darwin*)
		platform="darwin"
		;;
	MINGW* | MSYS* | CYGWIN*)
		platform="windows"
		;;
	*)
		log_error "Unsupported platform: $(uname -s)"
		exit 1
		;;
	esac
	echo "$platform"
}

# Detect architecture
detect_arch() {
	local arch
	case "$(uname -m)" in
	x86_64 | amd64)
		arch="amd64"
		;;
	aarch64 | arm64)
		arch="arm64"
		;;
	armv7l)
		arch="arm"
		;;
	*)
		log_error "Unsupported architecture: $(uname -m)"
		exit 1
		;;
	esac
	echo "$arch"
}

# Compare semantic versions (returns 0 if v1 >= v2, 1 otherwise)
version_ge() {
	local v1="$1"
	local v2="$2"

	# Remove 'v' prefix if present
	v1="${v1#v}"
	v2="${v2#v}"

	# Simple version comparison using sort -V
	if printf '%s\n%s\n' "$v2" "$v1" | sort -V -C 2>/dev/null; then
		return 0
	else
		return 1
	fi
}

# Fetch latest version from GitHub releases
get_github_latest_version() {
	local repo="$1"
	local version

	# Try using GitHub API (no auth required for public repos)
	version=$(curl -sSL "https://api.github.com/repos/$repo/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/')

	if [[ -z "$version" ]]; then
		log_warn "Could not fetch latest version for $repo"
		return 1
	fi

	echo "$version"
}

# Install kubectl
install_kubectl() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""
	local latest_version=""

	# Check current version
	if [[ -f "$OBOL_BIN_DIR/kubectl" ]]; then
		current_version=$("$OBOL_BIN_DIR/kubectl" version --client=true --output=json 2>/dev/null | grep gitVersion | head -1 | sed 's/.*"v\([0-9.]*\)".*/\1/' || echo "")
	fi

	# Get latest stable version
	latest_version=$(curl -sSL "https://dl.k8s.io/release/stable.txt" 2>/dev/null)
	latest_version="${latest_version#v}"

	if [[ -z "$latest_version" ]]; then
		log_warn "Could not determine latest kubectl version"
		return 1
	fi

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$latest_version"; then
		log_success "kubectl v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading kubectl from v$current_version to v$latest_version..."
	else
		log_info "Installing kubectl v$latest_version..."
	fi

	# Download kubectl
	local download_url="https://dl.k8s.io/release/v${latest_version}/bin/${platform}/${arch}/kubectl"

	if curl -sSL "$download_url" -o "$OBOL_BIN_DIR/kubectl.tmp"; then
		chmod +x "$OBOL_BIN_DIR/kubectl.tmp"
		mv "$OBOL_BIN_DIR/kubectl.tmp" "$OBOL_BIN_DIR/kubectl"
		log_success "kubectl v$latest_version installed"
	else
		log_error "Failed to download kubectl"
		rm -f "$OBOL_BIN_DIR/kubectl.tmp"
		return 1
	fi
}

# Install helm
install_helm() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""
	local latest_version=""

	# Check current version
	if [[ -f "$OBOL_BIN_DIR/helm" ]]; then
		current_version=$("$OBOL_BIN_DIR/helm" version --short 2>/dev/null | sed -n 's/v\([0-9.]*\).*/\1/p' || echo "")
	fi

	# Get latest version from GitHub
	latest_version=$(get_github_latest_version "helm/helm")
	latest_version="${latest_version#v}"

	if [[ -z "$latest_version" ]]; then
		log_warn "Could not determine latest helm version"
		return 1
	fi

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$latest_version"; then
		log_success "helm v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading helm from v$current_version to v$latest_version..."
	else
		log_info "Installing helm v$latest_version..."
	fi

	# Download and extract helm
	local tmp_dir=$(mktemp -d)
	local download_url="https://get.helm.sh/helm-v${latest_version}-${platform}-${arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/${platform}-${arch}/helm" "$OBOL_BIN_DIR/helm"
		chmod +x "$OBOL_BIN_DIR/helm"
		rm -rf "$tmp_dir"
		log_success "helm v$latest_version installed"
	else
		log_error "Failed to download helm"
		rm -rf "$tmp_dir"
		return 1
	fi
}

# Install k3d
install_k3d() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""
	local latest_version=""

	# Check current version
	if [[ -f "$OBOL_BIN_DIR/k3d" ]]; then
		current_version=$("$OBOL_BIN_DIR/k3d" version 2>/dev/null | sed -n 's/k3d version v\([0-9.]*\).*/\1/p' || echo "")
	fi

	# Get latest version from GitHub
	latest_version=$(get_github_latest_version "k3d-io/k3d")
	latest_version="${latest_version#v}"

	if [[ -z "$latest_version" ]]; then
		log_warn "Could not determine latest k3d version"
		return 1
	fi

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$latest_version"; then
		log_success "k3d v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading k3d from v$current_version to v$latest_version..."
	else
		log_info "Installing k3d v$latest_version..."
	fi

	# Map platform/arch to k3d naming
	local k3d_platform="$platform"
	local k3d_arch="$arch"

	# Download k3d
	local download_url="https://github.com/k3d-io/k3d/releases/download/v${latest_version}/k3d-${k3d_platform}-${k3d_arch}"

	if curl -sSL "$download_url" -o "$OBOL_BIN_DIR/k3d.tmp"; then
		chmod +x "$OBOL_BIN_DIR/k3d.tmp"
		mv "$OBOL_BIN_DIR/k3d.tmp" "$OBOL_BIN_DIR/k3d"
		log_success "k3d v$latest_version installed"
	else
		log_error "Failed to download k3d"
		rm -f "$OBOL_BIN_DIR/k3d.tmp"
		return 1
	fi
}

# Install all dependencies
install_dependencies() {
	log_info "Checking and installing dependencies..."
	echo ""

	# Install each dependency
	install_kubectl || log_warn "kubectl installation failed (continuing...)"
	install_helm || log_warn "helm installation failed (continuing...)"
	install_k3d || log_warn "k3d installation failed (continuing...)"

	echo ""
	log_success "Dependencies check complete"
}

# Check if OBOL_BIN_DIR is in PATH and print instructions if not
check_and_print_path_instructions() {
	# Skip in development mode
	if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
		return 0
	fi

	# Check if OBOL_BIN_DIR is already in PATH
	if echo "$PATH" | grep -q "$OBOL_BIN_DIR"; then
		log_success "OBOL_BIN_DIR already in PATH"
		return 0
	fi

	# Print instructions to add to PATH
	log_info "OBOL_BIN_DIR not found in PATH"
	echo ""
	echo "Add this line to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
	echo ""
	echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
}

# Print post-install instructions
print_instructions() {
	echo ""
	log_success "Obol Stack installation complete!"
	echo ""
	echo "Verify installation:"
	echo ""
	echo "  obol version"
	echo ""
	echo "To initialize a cluster, run:"
	echo ""
	echo "  obol stack init"
	echo "  obol stack up"
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
	check_and_print_path_instructions
	print_instructions

	echo ""
	log_success "Setup complete!"
}

# Run main
main
