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

	# Config directories
	mkdir -p "$OBOL_BIN_DIR"
	mkdir -p "$OBOL_CONFIG_DIR/cluster/k3d"
	mkdir -p "$OBOL_CONFIG_DIR/cluster/kubeconfig"

	# State directories (logs and history are created per-cluster by the obol binary)
	mkdir -p "$OBOL_STATE_DIR"

	# Data directories (persistent data)
	mkdir -p "$OBOL_DATA_DIR"

	log_success "Directories created"
}

# Install obol binary
install_obol_binary() {
	log_info "Installing obol binary..."

	# Check if obol binary already exists
	if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		local current_version
		current_version=$("$OBOL_BIN_DIR/obol" --version 2>/dev/null | sed -n 's/.*version \([0-9.]*\).*/\1/p' || echo "unknown")
		log_info "Found existing obol binary (version: $current_version)"
		log_info "Upgrading..."
	fi

	# For development, we'll build from source if go is available
	if command_exists go && [[ -f "cmd/obol/main.go" ]]; then
		log_info "Building from source..."
		CGO_ENABLED=0 go build -o "$OBOL_BIN_DIR/obol" ./cmd/obol
		chmod +x "$OBOL_BIN_DIR/obol"

		local new_version
		new_version=$("$OBOL_BIN_DIR/obol" --version 2>/dev/null | sed -n 's/.*version \([0-9.]*\).*/\1/p' || echo "unknown")
		log_success "Installed obol binary (version: $new_version)"
	else
		# In production, this would download from GitHub releases
		log_warn "Production binary download not yet implemented"
		log_info "Please build manually: go build -o $OBOL_BIN_DIR/obol ./cmd/obol"
		return 1
	fi
}

# Detect platform
detect_platform() {
	local platform
	case "$(uname -s)" in
		Linux*)
			# Check if running under WSL
			if grep -qi microsoft /proc/version 2>/dev/null; then
				platform="linux"  # WSL uses Linux binaries
			else
				platform="linux"
			fi
			;;
		Darwin*)
			platform="darwin"
			;;
		MINGW*|MSYS*|CYGWIN*)
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
		x86_64|amd64)
			arch="amd64"
			;;
		aarch64|arm64)
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

# Install helmfile
install_helmfile() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""
	local latest_version=""

	# Check current version
	if [[ -f "$OBOL_BIN_DIR/helmfile" ]]; then
		current_version=$("$OBOL_BIN_DIR/helmfile" version 2>/dev/null | grep "Version" | sed -n 's/.*Version[[:space:]]*\([0-9.]*\).*/\1/p' || echo "")
	fi

	# Get latest version from GitHub
	latest_version=$(get_github_latest_version "helmfile/helmfile")
	latest_version="${latest_version#v}"

	if [[ -z "$latest_version" ]]; then
		log_warn "Could not determine latest helmfile version"
		return 1
	fi

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$latest_version"; then
		log_success "helmfile v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading helmfile from v$current_version to v$latest_version..."
	else
		log_info "Installing helmfile v$latest_version..."
	fi

	# Map platform/arch to helmfile naming
	local helmfile_platform="$platform"
	local helmfile_arch="$arch"

	# Helmfile uses different naming for architectures
	if [[ "$arch" == "amd64" ]]; then
		helmfile_arch="amd64"
	elif [[ "$arch" == "arm64" ]]; then
		helmfile_arch="arm64"
	fi

	# Download and extract helmfile
	local tmp_dir=$(mktemp -d)
	local download_url="https://github.com/helmfile/helmfile/releases/download/v${latest_version}/helmfile_${latest_version}_${helmfile_platform}_${helmfile_arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/helmfile" "$OBOL_BIN_DIR/helmfile"
		chmod +x "$OBOL_BIN_DIR/helmfile"
		rm -rf "$tmp_dir"
		log_success "helmfile v$latest_version installed"
	else
		log_error "Failed to download helmfile"
		rm -rf "$tmp_dir"
		return 1
	fi
}

# Install helm-diff plugin
install_helm_diff() {
	# Ensure helm is installed first
	if [[ ! -f "$OBOL_BIN_DIR/helm" ]]; then
		log_warn "helm not found, skipping helm-diff plugin"
		return 1
	fi

	# Check if plugin is already installed
	if "$OBOL_BIN_DIR/helm" plugin list 2>/dev/null | grep -q "diff"; then
		log_success "helm-diff plugin already installed"
		return 0
	fi

	log_info "Installing helm-diff plugin..."

	# Install the plugin
	if "$OBOL_BIN_DIR/helm" plugin install https://github.com/databus23/helm-diff >/dev/null 2>&1; then
		log_success "helm-diff plugin installed"
	else
		log_error "Failed to install helm-diff plugin"
		return 1
	fi
}

# Install k9s
install_k9s() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""
	local latest_version=""

	# Check current version
	if [[ -f "$OBOL_BIN_DIR/k9s" ]]; then
		current_version=$("$OBOL_BIN_DIR/k9s" version --short 2>/dev/null | sed -n 's/.*v\([0-9.]*\).*/\1/p' | head -1 || echo "")
	fi

	# Get latest version from GitHub
	latest_version=$(get_github_latest_version "derailed/k9s")
	latest_version="${latest_version#v}"

	if [[ -z "$latest_version" ]]; then
		log_warn "Could not determine latest k9s version"
		return 1
	fi

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$latest_version"; then
		log_success "k9s v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading k9s from v$current_version to v$latest_version..."
	else
		log_info "Installing k9s v$latest_version..."
	fi

	# Map platform/arch to k9s naming
	local k9s_platform
	case "$platform" in
		darwin)
			k9s_platform="Darwin"
			;;
		linux)
			k9s_platform="Linux"
			;;
		windows)
			k9s_platform="Windows"
			;;
	esac

	local k9s_arch
	case "$arch" in
		amd64)
			k9s_arch="amd64"
			;;
		arm64)
			k9s_arch="arm64"
			;;
	esac

	# Download and extract k9s
	local tmp_dir=$(mktemp -d)
	local download_url="https://github.com/derailed/k9s/releases/download/v${latest_version}/k9s_${k9s_platform}_${k9s_arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/k9s" "$OBOL_BIN_DIR/k9s"
		chmod +x "$OBOL_BIN_DIR/k9s"
		rm -rf "$tmp_dir"
		log_success "k9s v$latest_version installed"
	else
		log_error "Failed to download k9s"
		rm -rf "$tmp_dir"
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
	install_helmfile || log_warn "helmfile installation failed (continuing...)"
	install_k9s || log_warn "k9s installation failed (continuing...)"
	install_helm_diff || log_warn "helm-diff plugin installation failed (continuing...)"

	echo ""
	log_success "Dependencies check complete"
}

# Prompt to update shell PATH
# Returns 0 if PATH was configured, 1 otherwise
update_shell_path() {
	# Skip in development mode
	if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
		PATH_CONFIGURED="true"
		return 0
	fi

	# Detect shell config file
	local shell_name=$(basename "$SHELL" 2>/dev/null || echo "")
	local config_file=""

	case "$shell_name" in
		bash)
			config_file="$HOME/.bashrc"
			;;
		zsh)
			config_file="$HOME/.zshrc"
			;;
		fish)
			config_file="$HOME/.config/fish/config.fish"
			;;
		*)
			# Unknown shell, skip
			PATH_CONFIGURED="false"
			return 1
			;;
	esac

	# Check if config file exists
	if [[ ! -f "$config_file" ]]; then
		PATH_CONFIGURED="false"
		return 1
	fi

	# Check if PATH entry already exists
	if grep -q "$OBOL_BIN_DIR" "$config_file" 2>/dev/null; then
		log_success "PATH already configured in $config_file"
		PATH_CONFIGURED="true"
		return 0
	fi

	# Prompt user
	echo ""
	log_info "Detected shell: $shell_name"
	echo ""
	read -p "Add $OBOL_BIN_DIR to PATH in $config_file? [y/N] " -r
	echo ""

	if [[ $REPLY =~ ^[Yy]$ ]]; then
		# Add PATH export to config file
		{
			echo ""
			echo "# Added by obolup installer"
			echo "export PATH=\"$OBOL_BIN_DIR:\$PATH\""
		} >> "$config_file"

		log_success "Added to PATH in $config_file"
		log_info "Reload your shell or run: source $config_file"
		PATH_CONFIGURED="true"
		return 0
	else
		log_info "Skipped PATH update"
		PATH_CONFIGURED="false"
		return 1
	fi
}

# Print post-install instructions
print_instructions() {
	echo ""
	log_success "Obol Stack installation complete!"
	echo ""

	# Only show manual instructions if PATH wasn't configured
	if [[ "${PATH_CONFIGURED}" != "true" ]]; then
		echo "Add this to your shell profile (~/.bashrc, ~/.zshrc, etc.):"
		echo ""
		echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
		echo ""
		echo "Then reload your shell or run:"
		echo ""
		echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
		echo ""
	fi

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
	update_shell_path
	print_instructions

	echo ""
	log_success "Setup complete!"
}

# Run main
main
