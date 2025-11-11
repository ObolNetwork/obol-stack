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
	# Use parameter expansion with default to handle curl | bash case
	if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
		SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	else
		# Fallback to current directory if BASH_SOURCE not available
		SCRIPT_DIR="$(pwd)"
	fi
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

# Pinned dependency versions
# Update these versions to upgrade dependencies across all installations
readonly KUBECTL_VERSION="1.31.0"
readonly HELM_VERSION="3.16.2"
readonly K3D_VERSION="5.8.3"
readonly HELMFILE_VERSION="0.169.1"
readonly K9S_VERSION="0.32.5"
readonly HELM_DIFF_VERSION="3.9.11"

# Repository URL for building from source
readonly OBOL_REPO_URL="git@github.com:ObolNetwork/obol-stack.git"

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

# Detect installation mode (install vs upgrade)
detect_installation_mode() {
	if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		echo "upgrade"
	else
		echo "install"
	fi
}

# Check Docker installation and availability
check_docker() {
	log_info "Checking Docker requirements..."

	# Check if docker command exists
	if ! command_exists docker; then
		log_error "Docker is not installed"
		echo ""
		echo "Obol Stack requires Docker to run k3d clusters."
		echo ""
		echo "Install Docker:"
		echo "  • Ubuntu/Debian: https://docs.docker.com/engine/install/ubuntu/"
		echo "  • macOS: https://docs.docker.com/desktop/install/mac-install/"
		echo "  • Other: https://docs.docker.com/engine/install/"
		echo ""
		return 1
	fi

	# Check if Docker daemon is running
	if ! docker info >/dev/null 2>&1; then
		log_error "Docker daemon is not running"
		echo ""
		echo "Please start the Docker daemon:"
		echo "  • Linux: sudo systemctl start docker"
		echo "  • macOS/Windows: Start Docker Desktop application"
		echo ""
		echo "Then run this installer again."
		echo ""
		return 1
	fi

	# Check Docker version (require at least 20.10.0 for k3d compatibility)
	local docker_version
	docker_version=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "0.0.0")

	# Extract major.minor version
	local major minor
	major=$(echo "$docker_version" | cut -d. -f1)
	minor=$(echo "$docker_version" | cut -d. -f2)

	if [[ "$major" -lt 20 ]] || { [[ "$major" -eq 20 ]] && [[ "$minor" -lt 10 ]]; }; then
		log_warn "Docker version $docker_version is older than recommended (20.10.0+)"
		log_warn "k3d may not work correctly with older Docker versions"
	fi

	# Check if user can run Docker without sudo (Linux-specific)
	if [[ "$(uname -s)" == "Linux" ]]; then
		if ! docker ps >/dev/null 2>&1; then
			log_warn "Current user cannot run Docker commands"
			echo ""
			echo "You may need to add your user to the docker group:"
			echo "  sudo usermod -aG docker \$USER"
			echo "  newgrp docker"
			echo ""
			echo "Or run commands with sudo."
			echo ""
			# Don't fail here - user might be running with sudo
		fi
	fi

	# Check Docker networking (ensure bridge network works)
	if ! docker network ls >/dev/null 2>&1; then
		log_error "Docker networking is not functional"
		echo ""
		echo "Docker networking is required for k3d clusters."
		echo "Please check your Docker installation."
		echo ""
		return 1
	fi

	log_success "Docker is installed and running (version $docker_version)"
	return 0
}

# Check if binary exists globally in PATH (excluding OBOL_BIN_DIR)
check_global_binary() {
	local binary_name="$1"

	# Get all matching binaries in PATH
	local all_paths
	all_paths=$(type -a -P "$binary_name" 2>/dev/null || true)

	# Find first path that's NOT in OBOL_BIN_DIR
	while IFS= read -r path; do
		if [[ -n "$path" && "$path" != "$OBOL_BIN_DIR/$binary_name" ]]; then
			# Found a global binary outside OBOL_BIN_DIR
			echo "$path"
			return 0
		fi
	done <<<"$all_paths"

	return 1
}

# Create symlink to global binary in OBOL_BIN_DIR
create_binary_symlink() {
	local binary_name="$1"
	local global_path="$2"
	local local_path="$OBOL_BIN_DIR/$binary_name"

	# Check if local path already exists
	if [[ -e "$local_path" || -L "$local_path" ]]; then
		# Check if it's already a symlink to the correct target
		if [[ -L "$local_path" ]]; then
			local current_target
			current_target=$(readlink "$local_path")
			if [[ "$current_target" == "$global_path" ]]; then
				# Already correctly symlinked
				return 0
			fi
		fi
		# Remove existing file/symlink
		rm -f "$local_path"
	fi

	# Create symlink
	if ln -s "$global_path" "$local_path"; then
		return 0
	else
		log_warn "Failed to create symlink for $binary_name"
		return 1
	fi
}

# Remove binary from OBOL_BIN_DIR if it's a broken symlink
remove_broken_symlink() {
	local binary_name="$1"
	local local_path="$OBOL_BIN_DIR/$binary_name"

	# Check if it's a symlink
	if [[ -L "$local_path" ]]; then
		# Check if the symlink is broken (target doesn't exist)
		if [[ ! -e "$local_path" ]]; then
			rm -f "$local_path"
			log_info "Removed broken symlink for $binary_name"
			return 0
		fi
	fi

	return 1
}

# Create directory structure
create_directories() {
	log_info "Creating directory structure..."

	# Config directories
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
	if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
		SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	else
		SCRIPT_DIR="$(pwd)"
	fi

	# Create wrapper script that uses 'go run'
	cat >"$OBOL_BIN_DIR/obol" <<'EOF'
#!/usr/bin/env bash
# Obol CLI Development Wrapper
# This script runs the obol CLI using 'go run' for rapid development

# Find the project root (where obolup.sh is located)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Run the CLI
cd "$SCRIPT_DIR" && exec go run -a ./cmd/obol "$@"
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

	# TODO Perhaps git and golang need to be installed in tmp dir in order to build from source

	log_info "Cloning repository..."
	if ! git clone --depth 1 --branch "$build_ref" "$OBOL_REPO_URL" "$tmp_dir" 2>/dev/null; then
		# If branch doesn't exist, try as a tag
		if ! git clone "$OBOL_REPO_URL" "$tmp_dir" 2>/dev/null; then
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
	# Get current version if exists (for upgrade messaging)
	local current_version=""
	if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		current_version=$("$OBOL_BIN_DIR/obol" version 2>/dev/null | head -1 || echo "")
	fi

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
		local latest_tag=""
		if command_exists curl; then
			# macOS-compatible: use sed instead of grep -oP
			# Disable errexit temporarily to handle curl failure gracefully
			set +e
			latest_tag=$(curl -fsSL https://api.github.com/repos/ObolNetwork/obol-stack/releases/latest 2>/dev/null | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p')
			set -e
		fi

		# If we got a tag, try to download it
		if [[ -n "$latest_tag" ]]; then
			if download_release "$latest_tag"; then
				show_version_change "$current_version"
				return 0
			fi
			log_warn "Download failed, falling back to building from source..."
		else
			log_info "No releases found, building from source..."
		fi

		# Fallback: build from source
		build_from_source "main"
		show_version_change "$current_version"
	else
		# Specific release requested
		log_info "Attempting to download release: $release"
		if download_release "$release"; then
			show_version_change "$current_version"
			return 0
		fi

		log_warn "Release $release not found, building from source..."
		build_from_source "$release"
		show_version_change "$current_version"
	fi
}

# Show version change after upgrade
show_version_change() {
	local old_version="$1"

	# Get new version
	local new_version=""
	if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		new_version=$("$OBOL_BIN_DIR/obol" version 2>/dev/null | head -1 || echo "")
	fi

	# Show upgrade message if versions are different
	if [[ -n "$old_version" && -n "$new_version" ]]; then
		if [[ "$old_version" != "$new_version" ]]; then
			echo ""
			log_success "Upgraded: $old_version → $new_version"
		else
			echo ""
			log_success "Already at version: $new_version"
		fi
	elif [[ -n "$new_version" ]]; then
		echo ""
		log_success "Installed version: $new_version"
	fi
}

# Copy bootstrap script to bin directory for easy upgrades
copy_bootstrap_script() {
	# Skip in development mode
	if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
		log_info "Development mode: skipping bootstrap script copy"
		return 0
	fi

	# Skip if we're already running from OBOL_BIN_DIR (avoid self-copy loop)
	local script_path=""
	if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
		script_path="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
		if [[ "$script_path" == "$OBOL_BIN_DIR/"* ]]; then
			log_info "Already running from OBOL_BIN_DIR, skipping self-copy"
			return 0
		fi
	fi

	# Check if running from stdin (piped from curl) vs from a file
	local script_source_url="https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/obolup.sh"

	if [[ -z "${BASH_SOURCE[0]:-}" ]] || [[ ! -f "${BASH_SOURCE[0]}" ]]; then
		# Running from stdin (curl | bash) - download the script
		log_info "Downloading bootstrap script to $OBOL_BIN_DIR/obolup.sh..."

		if curl -fsSL "$script_source_url" -o "$OBOL_BIN_DIR/obolup.sh"; then
			chmod +x "$OBOL_BIN_DIR/obolup.sh"
			log_success "Bootstrap script installed at $OBOL_BIN_DIR/obolup.sh"
			log_info "To upgrade in future, run: obolup.sh"
		else
			log_warn "Failed to download bootstrap script (non-critical)"
			log_info "You can manually download it later with:"
			echo ""
			echo "  curl -sSL $script_source_url -o $OBOL_BIN_DIR/obolup.sh"
			echo "  chmod +x $OBOL_BIN_DIR/obolup.sh"
			echo ""
		fi
	else
		# Running from a file - copy it
		log_info "Copying bootstrap script to $OBOL_BIN_DIR/obolup.sh..."

		if cp "${BASH_SOURCE[0]}" "$OBOL_BIN_DIR/obolup.sh"; then
			chmod +x "$OBOL_BIN_DIR/obolup.sh"
			log_success "Bootstrap script installed at $OBOL_BIN_DIR/obolup.sh"
			log_info "To upgrade in future, run: obolup.sh"
		else
			log_warn "Failed to copy bootstrap script (non-critical)"
		fi
	fi
}

# Detect platform
detect_platform() {
	local platform
	case "$(uname -s)" in
	Linux*)
		platform="linux"
		;;
	Darwin*)
		platform="darwin"
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

# Install kubectl
install_kubectl() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""

	# Remove broken symlink if exists
	remove_broken_symlink "kubectl"

	# Check for global kubectl first
	local global_kubectl
	if global_kubectl=$(check_global_binary "kubectl"); then
		local global_version
		global_version=$("$global_kubectl" version --client=true --output=json 2>/dev/null | grep gitVersion | head -1 | sed 's/.*"v\([0-9.]*\)".*/\1/' || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$KUBECTL_VERSION"; then
			if create_binary_symlink "kubectl" "$global_kubectl"; then
				log_success "kubectl v$global_version already installed at: $global_kubectl (symlinked)"
			else
				log_success "kubectl v$global_version already installed at: $global_kubectl"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/kubectl" ]]; then
		current_version=$("$OBOL_BIN_DIR/kubectl" version --client=true --output=json 2>/dev/null | grep gitVersion | head -1 | sed 's/.*"v\([0-9.]*\)".*/\1/' || echo "")
	fi

	# Use pinned version
	local target_version="$KUBECTL_VERSION"

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "kubectl v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading kubectl from v$current_version to v$target_version..."
	else
		log_info "Installing kubectl v$target_version..."
	fi

	# Download kubectl
	local download_url="https://dl.k8s.io/release/v${target_version}/bin/${platform}/${arch}/kubectl"

	if curl -sSL "$download_url" -o "$OBOL_BIN_DIR/kubectl.tmp"; then
		chmod +x "$OBOL_BIN_DIR/kubectl.tmp"
		mv "$OBOL_BIN_DIR/kubectl.tmp" "$OBOL_BIN_DIR/kubectl"
		log_success "kubectl v$target_version installed"
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

	# Remove broken symlink if exists
	remove_broken_symlink "helm"

	# Check for global helm first
	local global_helm
	if global_helm=$(check_global_binary "helm"); then
		local global_version
		global_version=$("$global_helm" version --short 2>/dev/null | sed -n 's/v\([0-9.]*\).*/\1/p' || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$HELM_VERSION"; then
			if create_binary_symlink "helm" "$global_helm"; then
				log_success "helm v$global_version already installed at: $global_helm (symlinked)"
			else
				log_success "helm v$global_version already installed at: $global_helm"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/helm" ]]; then
		current_version=$("$OBOL_BIN_DIR/helm" version --short 2>/dev/null | sed -n 's/v\([0-9.]*\).*/\1/p' || echo "")
	fi

	# Use pinned version
	local target_version="$HELM_VERSION"

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "helm v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading helm from v$current_version to v$target_version..."
	else
		log_info "Installing helm v$target_version..."
	fi

	# Download and extract helm
	local tmp_dir=$(mktemp -d)
	local download_url="https://get.helm.sh/helm-v${target_version}-${platform}-${arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/${platform}-${arch}/helm" "$OBOL_BIN_DIR/helm"
		chmod +x "$OBOL_BIN_DIR/helm"
		rm -rf "$tmp_dir"
		log_success "helm v$target_version installed"
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

	# Remove broken symlink if exists
	remove_broken_symlink "k3d"

	# Check for global k3d first
	local global_k3d
	if global_k3d=$(check_global_binary "k3d"); then
		local global_version
		global_version=$("$global_k3d" version 2>/dev/null | sed -n 's/k3d version v\([0-9.]*\).*/\1/p' || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$K3D_VERSION"; then
			if create_binary_symlink "k3d" "$global_k3d"; then
				log_success "k3d v$global_version already installed at: $global_k3d (symlinked)"
			else
				log_success "k3d v$global_version already installed at: $global_k3d"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/k3d" ]]; then
		current_version=$("$OBOL_BIN_DIR/k3d" version 2>/dev/null | sed -n 's/k3d version v\([0-9.]*\).*/\1/p' || echo "")
	fi

	# Use pinned version
	local target_version="$K3D_VERSION"

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "k3d v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading k3d from v$current_version to v$target_version..."
	else
		log_info "Installing k3d v$target_version..."
	fi

	# Map platform/arch to k3d naming
	local k3d_platform="$platform"
	local k3d_arch="$arch"

	# Download k3d
	local download_url="https://github.com/k3d-io/k3d/releases/download/v${target_version}/k3d-${k3d_platform}-${k3d_arch}"

	if curl -sSL "$download_url" -o "$OBOL_BIN_DIR/k3d.tmp"; then
		chmod +x "$OBOL_BIN_DIR/k3d.tmp"
		mv "$OBOL_BIN_DIR/k3d.tmp" "$OBOL_BIN_DIR/k3d"
		log_success "k3d v$target_version installed"
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

	# Remove broken symlink if exists
	remove_broken_symlink "helmfile"

	# Check for global helmfile first
	local global_helmfile
	if global_helmfile=$(check_global_binary "helmfile"); then
		local global_version
		global_version=$("$global_helmfile" version 2>/dev/null | sed -n 's/.*Version[[:space:]]*v*\([0-9.]*\).*/\1/p' | head -1 || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$HELMFILE_VERSION"; then
			if create_binary_symlink "helmfile" "$global_helmfile"; then
				log_success "helmfile v$global_version already installed at: $global_helmfile (symlinked)"
			else
				log_success "helmfile v$global_version already installed at: $global_helmfile"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/helmfile" ]]; then
		current_version=$("$OBOL_BIN_DIR/helmfile" version 2>/dev/null | sed -n 's/.*Version[[:space:]]*v*\([0-9.]*\).*/\1/p' | head -1 || echo "")
	fi

	# Use pinned version
	local target_version="$HELMFILE_VERSION"

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "helmfile v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading helmfile from v$current_version to v$target_version..."
	else
		log_info "Installing helmfile v$target_version..."
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
	local download_url="https://github.com/helmfile/helmfile/releases/download/v${target_version}/helmfile_${target_version}_${helmfile_platform}_${helmfile_arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/helmfile" "$OBOL_BIN_DIR/helmfile"
		chmod +x "$OBOL_BIN_DIR/helmfile"
		rm -rf "$tmp_dir"
		log_success "helmfile v$target_version installed"
	else
		log_error "Failed to download helmfile"
		rm -rf "$tmp_dir"
		return 1
	fi
}

# Install helm-diff plugin
install_helm_diff() {
	# Find helm binary (check OBOL_BIN_DIR first, then global PATH)
	local helm_bin="$OBOL_BIN_DIR/helm"
	if [[ ! -f "$helm_bin" ]]; then
		helm_bin=$(check_global_binary "helm") || {
			log_warn "helm not found, skipping helm-diff plugin"
			return 1
		}
	fi

	# Try to check plugin list - if this fails, the plugin directory may be corrupted
	local plugin_check
	plugin_check=$("$helm_bin" plugin list 2>&1)
	local check_exit=$?

	# If plugin check succeeded and diff is already installed, we're done
	if [[ $check_exit -eq 0 ]] && echo "$plugin_check" | grep -q "diff"; then
		log_success "helm-diff plugin already installed"
		return 0
	fi

	# If plugin check failed with corruption error, try to remove the corrupted plugin
	if echo "$plugin_check" | grep -q "failed to load plugin"; then
		log_info "Detected corrupted helm-diff plugin, attempting to clean up..."

		# Get helm plugin directory (usually ~/.local/share/helm/plugins)
		local helm_plugins_dir
		helm_plugins_dir=$(dirname "$("$helm_bin" env 2>/dev/null | grep HELM_PLUGINS | cut -d'=' -f2 | tr -d '"' || echo "$HOME/.local/share/helm/plugins")")

		# Remove corrupted helm-diff plugin if it exists
		if [[ -d "$helm_plugins_dir/plugins/helm-diff" ]]; then
			rm -rf "$helm_plugins_dir/plugins/helm-diff"
			log_info "Removed corrupted plugin"
		fi
	fi

	log_info "Installing helm-diff plugin v${HELM_DIFF_VERSION}..."

	# Install the plugin with pinned version
	if "$helm_bin" plugin install https://github.com/databus23/helm-diff --version "v${HELM_DIFF_VERSION}" 2>&1 | grep -q "Installed plugin"; then
		log_success "helm-diff plugin v${HELM_DIFF_VERSION} installed"
		return 0
	else
		log_warn "helm-diff plugin installation failed (non-critical, continuing...)"
		return 1
	fi
}

# Install k9s
install_k9s() {
	local platform=$(detect_platform)
	local arch=$(detect_arch)
	local current_version=""

	# Remove broken symlink if exists
	remove_broken_symlink "k9s"

	# Check for global k9s first
	local global_k9s
	if global_k9s=$(check_global_binary "k9s"); then
		local global_version
		global_version=$("$global_k9s" version --short 2>/dev/null | sed -n 's/.*v\([0-9.]*\).*/\1/p' | head -1 || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$K9S_VERSION"; then
			if create_binary_symlink "k9s" "$global_k9s"; then
				log_success "k9s v$global_version already installed at: $global_k9s (symlinked)"
			else
				log_success "k9s v$global_version already installed at: $global_k9s"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/k9s" ]]; then
		current_version=$("$OBOL_BIN_DIR/k9s" version --short 2>/dev/null | sed -n 's/.*v\([0-9.]*\).*/\1/p' | head -1 || echo "")
	fi

	# Use pinned version
	local target_version="$K9S_VERSION"

	# Check if update needed
	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "k9s v$current_version is up to date"
		return 0
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading k9s from v$current_version to v$target_version..."
	else
		log_info "Installing k9s v$target_version..."
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
	local download_url="https://github.com/derailed/k9s/releases/download/v${target_version}/k9s_${k9s_platform}_${k9s_arch}.tar.gz"

	if curl -sSL "$download_url" | tar xz -C "$tmp_dir" 2>/dev/null; then
		mv "$tmp_dir/k9s" "$OBOL_BIN_DIR/k9s"
		chmod +x "$OBOL_BIN_DIR/k9s"
		rm -rf "$tmp_dir"
		log_success "k9s v$target_version installed"
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

# Check if obol.stack hostname is configured in /etc/hosts
check_hosts_file() {
	log_info "Checking /etc/hosts for obol.stack entry..."

	# Check if /etc/hosts contains obol.stack pointing to localhost
	if grep -q "obol.stack" /etc/hosts 2>/dev/null; then
		# Check if it points to localhost (127.0.0.1 or ::1)
		if grep -E "^(127\.0\.0\.1|::1)[[:space:]].*obol\.stack" /etc/hosts >/dev/null 2>&1; then
			log_success "obol.stack already configured in /etc/hosts"
			return 0
		else
			log_warn "obol.stack found in /etc/hosts but not pointing to localhost"
			return 1
		fi
	fi

	# Entry not found
	return 1
}

# Add obol.stack entry to /etc/hosts
update_hosts_file() {
	log_info "Adding obol.stack to /etc/hosts..."

	local hosts_entry="127.0.0.1 obol.stack"

	# Check if sudo is available
	if ! command_exists sudo; then
		log_error "sudo not available, cannot update /etc/hosts automatically"
		echo ""
		echo "Please manually add this line to /etc/hosts:"
		echo ""
		echo "  $hosts_entry"
		echo ""
		return 1
	fi

	# Check if we need password (sudo -n tests non-interactive)
	if sudo -n true 2>/dev/null; then
		# Already have sudo privileges or NOPASSWD configured
		log_info "Updating /etc/hosts with existing privileges..."
	else
		# Will need password - inform user
		echo ""
		log_warn "Administrator privileges required to update /etc/hosts"
		echo ""
		echo "Please enter your password when prompted to add:"
		echo "  $hosts_entry"
		echo ""
	fi

	# Try to append to /etc/hosts with sudo
	if echo "$hosts_entry" | sudo tee -a /etc/hosts >/dev/null 2>&1; then
		log_success "Added obol.stack to /etc/hosts"
		return 0
	else
		log_warn "Failed to update /etc/hosts"
		echo ""
		echo "Please manually add this line to /etc/hosts:"
		echo ""
		echo "  $hosts_entry"
		echo ""
		echo "Example command:"
		echo "  echo '$hosts_entry' | sudo tee -a /etc/hosts"
		echo ""
		return 1
	fi
}

# Check and configure /etc/hosts entry for obol.stack
configure_hosts_file() {
	if ! check_hosts_file; then
		update_hosts_file
	fi
}

# Detect appropriate shell profile file (NVM-style detection)
detect_shell_profile() {
	local profile=""

	# Check for environment override
	if [[ -n "${PROFILE:-}" ]] && [[ -f "${PROFILE}" ]]; then
		echo "${PROFILE}"
		return 0
	fi

	# Shell-specific detection based on $SHELL
	if [[ "${SHELL}" == *"bash"* ]]; then
		# Bash: prefer .bashrc (interactive shells), fallback to .bash_profile (login shells)
		if [[ -f "$HOME/.bashrc" ]]; then
			echo "$HOME/.bashrc"
			return 0
		elif [[ -f "$HOME/.bash_profile" ]]; then
			echo "$HOME/.bash_profile"
			return 0
		fi
	elif [[ "${SHELL}" == *"zsh"* ]]; then
		# Zsh: prefer .zshrc (interactive), fallback to .zprofile (login)
		local zdotdir="${ZDOTDIR:-$HOME}"
		if [[ -f "$zdotdir/.zshrc" ]]; then
			echo "$zdotdir/.zshrc"
			return 0
		elif [[ -f "$zdotdir/.zprofile" ]]; then
			echo "$zdotdir/.zprofile"
			return 0
		fi
	fi

	# Fallback: scan for first existing file
	for rc in .profile .bashrc .bash_profile .zprofile .zshrc; do
		if [[ -f "$HOME/$rc" ]]; then
			echo "$HOME/$rc"
			return 0
		fi
	done

	# Default: .bashrc for interactive shells (most common)
	echo "$HOME/.bashrc"
}

# Print manual PATH configuration instructions
print_path_instructions() {
	local profile_file="$1"

	echo ""
	log_info "Manual setup instructions:"
	echo ""
	echo "Add this line to your shell profile ($profile_file):"
	echo ""
	echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
	echo "Then reload your profile:"
	echo ""
	echo "  source $profile_file"
	echo ""
	echo "Or export for current session only:"
	echo ""
	echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
}

# Add PATH export to profile file
add_to_profile() {
	local profile="$1"
	local path_export="export PATH=\"$OBOL_BIN_DIR:\$PATH\""

	log_info "Adding to PATH in $profile"

	# Create profile directory if needed
	mkdir -p "$(dirname "$profile")"

	# Create file if it doesn't exist
	touch "$profile"

	# Add PATH export with comment
	{
		echo ""
		echo "# Added by Obol Stack installer"
		echo "$path_export"
	} >>"$profile"

	log_success "Added to PATH in $profile"
}

# Configure PATH with shell detection and user consent
# Detects the appropriate shell profile file based on $SHELL and existing files.
# In interactive mode, asks user whether to auto-modify or show manual instructions.
# In non-interactive mode (CI/CD), prints manual instructions only unless OBOL_MODIFY_PATH=yes.
configure_path() {
	# Check if OBOL_BIN_DIR is already in current PATH
	if echo "$PATH" | grep -q "$OBOL_BIN_DIR"; then
		log_success "OBOL_BIN_DIR already in PATH"
		return 0
	fi

	# Detect appropriate profile file
	local profile
	profile=$(detect_shell_profile)

	# Check if already configured in detected profile
	if [[ -f "$profile" ]] && grep -qF "$OBOL_BIN_DIR" "$profile" 2>/dev/null; then
		log_success "OBOL_BIN_DIR already configured in $profile"
		log_info "Will be available in new shell sessions"
		return 0
	fi

	# Interactive terminal: ask for consent
	if [[ -t 0 ]]; then
		echo ""
		log_info "To use 'obol' command, $OBOL_BIN_DIR needs to be in your PATH"
		echo ""
		echo "Detected shell profile: $profile"
		echo ""
		echo "Options:"
		echo "  1. Automatically add to $profile (recommended)"
		echo "  2. Show manual instructions"
		echo ""

		local choice
		read -p "Choose [1/2]: " choice

		case "$choice" in
		1)
			add_to_profile "$profile"
			echo ""
			log_info "PATH updated for future sessions"
			log_info "To use immediately in this session, run:"
			echo ""
			echo "  export PATH=\"$OBOL_BIN_DIR:\$PATH\""
			echo ""
			;;
		2)
			print_path_instructions "$profile"
			;;
		*)
			print_path_instructions "$profile"
			;;
		esac
	else
		# Non-interactive: check environment variable override
		if [[ "${OBOL_MODIFY_PATH:-no}" == "yes" ]]; then
			add_to_profile "$profile"
			log_info "Will be available in new shell sessions"
		else
			# Default: print instructions for non-interactive contexts
			print_path_instructions "$profile"
		fi
	fi
}

# Print post-install instructions
print_instructions() {
	local install_mode="$1"

	echo ""
	if [[ "$install_mode" == "upgrade" ]]; then
		log_success "Obol Stack upgrade complete!"
	else
		log_success "Obol Stack installation complete!"
	fi
	echo ""

	# Check if terminal is interactive for bootstrap prompt
	if [[ -t 0 ]] && [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		echo ""
		log_info "Would you like to start the cluster now?"
		echo ""
		echo "This will:"
		echo "  • Initialize cluster configuration"
		echo "  • Start the Obol Stack"
		echo "  • Open your browser to http://obol.stack"
		echo ""

		local choice
		read -p "Start cluster now? [y/N]: " choice

		case "$choice" in
		[Yy]*)
			echo ""
			log_info "Starting bootstrap process..."

			# Run obol bootstrap
			if "$OBOL_BIN_DIR/obol" bootstrap; then
				# Bootstrap succeeded, we're done
				return 0
			else
				log_error "Bootstrap failed"
				echo ""
				log_info "You can start the cluster manually with:"
				echo ""
				echo "  obol stack init"
				echo "  obol stack up"
				echo ""
				return 1
			fi
			;;
		*)
			# User declined, show manual instructions
			echo ""
			log_info "To start the cluster later, run:"
			echo ""
			echo "  obol stack init"
			echo "  obol stack up"
			echo ""
			log_info "Then open your browser to: http://obol.stack"
			echo ""
			;;
		esac
	else
		# Non-interactive or no binary - show manual instructions
		echo "Verify installation:"
		echo ""
		echo "  obol version"
		echo ""
		echo "To start the cluster, run:"
		echo ""
		echo "  obol stack init"
		echo "  obol stack up"
		echo ""
		log_info "Then open your browser to: http://obol.stack"
		echo ""
	fi
}

# Main installation flow
main() {
	# Prevent recursive installation loops
	if [[ "${OBOL_INSTALLING:-}" == "true" ]]; then
		log_error "Installation already in progress (recursive loop detected)"
		log_error "This usually means obolup.sh was called from within itself"
		exit 1
	fi
	export OBOL_INSTALLING=true

	echo ""
	echo "╔═══════════════════════════════════════════╗"
	echo "║                                           ║"
	echo "║     Obol Stack Bootstrap Installer        ║"
	echo "║                                           ║"
	echo "╚═══════════════════════════════════════════╝"
	echo ""

	# Detect installation mode
	local install_mode
	install_mode=$(detect_installation_mode)

	if [[ "$install_mode" == "upgrade" ]]; then
		log_info "Existing installation detected - upgrading..."
		# Show current version if available
		if [[ -f "$OBOL_BIN_DIR/obol" ]]; then
			local current_version
			current_version=$("$OBOL_BIN_DIR/obol" version 2>/dev/null || echo "unknown")
			if [[ -n "$current_version" ]]; then
				log_info "Current version: $current_version"
			fi
		fi
	else
		log_info "Fresh installation starting..."
	fi

	# Check Docker prerequisites first
	if ! check_docker; then
		log_error "Docker requirements not met"
		exit 1
	fi
	echo ""

	create_directories
	install_obol_binary
	copy_bootstrap_script
	install_dependencies
	configure_hosts_file
	configure_path
	print_instructions "$install_mode"

	echo ""
	log_success "Setup complete!"
}

# Run main
main
