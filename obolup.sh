#!/usr/bin/env bash

set -euo pipefail

# obolup.sh - Bootstrap installer for Obol Stack
# Usage: curl -sSL https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/obolup.sh | bash

# Obol brand colors (24-bit true color — blog.obol.org/branding)
# Degrades gracefully: modern terminals render exact hex, older ones approximate.
OBOL_GREEN='\033[38;2;47;228;171m'     # #2FE4AB — primary brand green
OBOL_DARK_GREEN='\033[38;2;15;124;118m' # #0F7C76 — dark green (success)
OBOL_CYAN='\033[38;2;60;210;221m'      # #3CD2DD — info / cyan
OBOL_PURPLE='\033[38;2;145;103;228m'   # #9167E4 — accent purple
OBOL_AMBER='\033[38;2;250;186;90m'     # #FABA5A — warning amber
OBOL_RED='\033[38;2;221;96;60m'        # #DD603C — error red-orange
OBOL_MUTED='\033[38;2;102;122;128m'    # #667A80 — dim / muted gray
BOLD='\033[1m'
NC='\033[0m' # No Color

# Development mode detection
if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
	# Get script directory for development mode
	# Use -f to check for a real file, not /dev/fd/* from process substitution
	if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
		SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	else
		# Fallback to current directory for curl | bash or bash <(...) cases
		SCRIPT_DIR="$(pwd)"
	fi
	WORKSPACE_DIR="$SCRIPT_DIR/.workspace"

	# Override directories to use local .workspace
	OBOL_CONFIG_DIR="${OBOL_CONFIG_DIR:-$WORKSPACE_DIR/config}"
	OBOL_DATA_DIR="${OBOL_DATA_DIR:-$WORKSPACE_DIR/data}"
	OBOL_STATE_DIR="${OBOL_STATE_DIR:-$WORKSPACE_DIR/state}"
	OBOL_BIN_DIR="${OBOL_BIN_DIR:-$WORKSPACE_DIR/bin}"

	log_warn() { echo -e "  ${OBOL_AMBER}${BOLD}!${NC} $1"; }
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
readonly KUBECTL_VERSION="1.35.0"
readonly HELM_VERSION="3.19.4"
readonly K3D_VERSION="5.8.3"
readonly HELMFILE_VERSION="1.2.3"
readonly K9S_VERSION="0.50.18"
readonly HELM_DIFF_VERSION="3.14.1"
# Must match internal/openclaw/OPENCLAW_VERSION (without "v" prefix).
# Tested by TestOpenClawVersionConsistency.
readonly OPENCLAW_VERSION="2026.3.24"

# Repository URL for building from source
readonly OBOL_REPO_URL="git@github.com:ObolNetwork/obol-stack.git"

# Logging functions — matching the Go CLI's ui package output style.
# Info/Error are top-level (no indent), Success/Warn are subordinate (2-space indent).
log_info() {
	echo -e "${OBOL_GREEN}${BOLD}==>${NC} $1"
}

log_success() {
	echo -e "  ${OBOL_GREEN}${BOLD}✓${NC} $1"
}

log_warn() {
	echo -e "  ${OBOL_AMBER}${BOLD}!${NC} $1"
}

log_error() {
	echo -e "${OBOL_RED}${BOLD}✗${NC} $1"
}

# Print dimmed secondary text (matches Go CLI's u.Dim)
log_dim() {
	echo -e "${OBOL_MUTED}$1${NC}"
}


# Check if command exists
command_exists() {
	command -v "$1" >/dev/null 2>&1
}

# Check host prerequisites that the installer cannot provide itself.
# Binaries we download (helm, kubectl, k3d, etc.) are not checked here —
# only tools the user must install separately.
check_prerequisites() {
	local missing=()

	# Node.js 22+ / npm — required for openclaw CLI (unless already installed)
	local need_npm=true
	if command_exists openclaw; then
		local oc_version
		oc_version=$(openclaw --version 2>/dev/null | tr -d '[:space:]' || echo "")
		if [[ -n "$oc_version" ]] && version_ge "$oc_version" "$OPENCLAW_VERSION"; then
			need_npm=false
		fi
	fi

	if [[ "$need_npm" == "true" ]]; then
		if ! command_exists npm; then
			missing+=("Node.js 22+ (npm) — required to install openclaw CLI")
		else
			local node_major
			node_major=$(node --version 2>/dev/null | sed 's/v//' | cut -d. -f1)
			if [[ -z "$node_major" ]] || [[ "$node_major" -lt 22 ]]; then
				missing+=("Node.js 22+ (found: v${node_major:-none}) — required for openclaw CLI")
			fi
		fi
	fi

	if [[ ${#missing[@]} -gt 0 ]]; then
		log_error "Missing prerequisites:"
		echo ""
		for dep in "${missing[@]}"; do
			echo "  • $dep"
		done
		echo ""
		log_dim "  Install the above, then re-run obolup.sh"
		echo ""
		return 1
	fi

	return 0
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
		echo "  Obol Stack requires Docker to run k3d clusters."
		echo ""
		echo "  Install Docker:"
		echo "    • Ubuntu/Debian: https://docs.docker.com/engine/install/ubuntu/"
		echo "    • macOS: https://docs.docker.com/desktop/install/mac-install/"
		echo "    • Other: https://docs.docker.com/engine/install/"
		echo ""
		return 1
	fi

	# Check if Docker daemon is running and accessible
	if ! docker info >/dev/null 2>&1; then
		# Distinguish permission errors from daemon-not-running
		local docker_err
		docker_err=$(docker info 2>&1)

		if echo "$docker_err" | grep -qi "permission denied"; then
			log_error "Docker is installed but your user does not have permission to access it"
			echo ""
			echo "  Add your user to the docker group and apply the change:"
			echo "    sudo usermod -aG docker \$USER"
			echo "    newgrp docker"
			echo ""
			log_dim "  Then run this installer again."
			echo ""
			return 1
		fi

		if [[ "$(uname -s)" == "Linux" ]]; then
			log_warn "Docker daemon is not running — attempting to start..."
			# Try systemd first (apt/yum installs), then snap
			if command_exists systemctl && systemctl list-unit-files docker.service >/dev/null 2>&1; then
				sudo systemctl start docker 2>/dev/null && sleep 2
			elif snap list docker >/dev/null 2>&1; then
				sudo snap start docker 2>/dev/null && sleep 3
			fi
		elif [[ "$(uname -s)" == "Darwin" ]]; then
			# Start Docker Desktop if it's installed but not running
			if [[ -d "/Applications/Docker.app" ]]; then
				log_warn "Docker Desktop is not running — starting it now..."
				open -a Docker
				# Docker Desktop can take a while to initialise; poll until ready
				local wait_secs=0
				while ! docker info >/dev/null 2>&1; do
					if [[ $wait_secs -ge 60 ]]; then
						break
					fi
					sleep 2
					wait_secs=$((wait_secs + 2))
				done
			fi
		fi

		# Re-check after start attempt
		if ! docker info >/dev/null 2>&1; then
			log_error "Docker daemon is not running"
			echo ""
			echo "  Please start the Docker daemon:"
			if [[ "$(uname -s)" == "Linux" ]]; then
				echo "    • systemd:  sudo systemctl start docker"
				echo "    • snap:     sudo snap start docker"
			else
				echo "    • macOS/Windows: Start Docker Desktop application"
			fi
			echo ""
			log_dim "  Then run this installer again."
			echo ""
			return 1
		fi
		log_success "Docker daemon started"
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

	# Detect and prevent circular symlinks.
	# This can happen if a global binary path (e.g. from a previous dev install)
	# already points to the location this script is trying to manage.
	if [[ -L "$global_path" ]]; then
		local link_target
		link_target=$(readlink "$global_path" || echo "") # Handle readlink failure

		if [[ "$link_target" == "$local_path" ]]; then
			log_warn "Circular symlink detected for $binary_name."
			log_warn "  $global_path -> $local_path"
			log_warn "Ignoring global binary to prevent loop. Will install fresh copy."
			return 1 # Signal failure to prevent using this global binary
		fi
	fi

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

# Install openclaw CLI
# Unlike other tools, openclaw has no standalone binary downloads.
# It's distributed as an npm package, so we install it locally into
# OBOL_BIN_DIR using npm --prefix to keep it workspace-contained.
install_openclaw() {
	local current_version=""
	local target_version="$OPENCLAW_VERSION"

	# Remove broken symlink if exists
	remove_broken_symlink "openclaw"

	# Check for global openclaw first (same pattern as kubectl, helm, etc.)
	local global_openclaw
	if global_openclaw=$(check_global_binary "openclaw"); then
		local global_version
		global_version=$("$global_openclaw" --version 2>/dev/null | tr -d '[:space:]' || echo "")
		if [[ -n "$global_version" ]] && version_ge "$global_version" "$target_version"; then
			if create_binary_symlink "openclaw" "$global_openclaw"; then
				log_success "openclaw v$global_version already installed at: $global_openclaw (symlinked)"
			else
				log_success "openclaw v$global_version already installed at: $global_openclaw"
			fi
			return 0
		fi
	fi

	# Check current version in OBOL_BIN_DIR
	if [[ -f "$OBOL_BIN_DIR/openclaw" ]]; then
		current_version=$("$OBOL_BIN_DIR/openclaw" --version 2>/dev/null | tr -d '[:space:]' || echo "")
	fi

	if [[ -n "$current_version" ]] && version_ge "$current_version" "$target_version"; then
		log_success "openclaw v$current_version is up to date"
		return 0
	fi

	# Require Node.js 22+ and npm
	if ! command_exists npm; then
		log_warn "npm not found — cannot install openclaw CLI"
		echo ""
		echo "  Install Node.js 22+ first, then re-run obolup.sh"
		echo "  Or install manually: npm install -g openclaw@$target_version"
		echo ""
		return 1
	fi

	local node_major
	node_major=$(node --version 2>/dev/null | sed 's/v//' | cut -d. -f1)
	if [[ -z "$node_major" ]] || [[ "$node_major" -lt 22 ]]; then
		log_warn "Node.js 22+ required for openclaw (found: v${node_major:-none})"
		echo ""
		echo "  Upgrade Node.js, then re-run obolup.sh"
		echo "  Or install manually: npm install -g openclaw@$target_version"
		echo ""
		return 1
	fi

	if [[ -n "$current_version" ]]; then
		log_info "Upgrading openclaw from v$current_version to v$target_version..."
	else
		log_info "Installing openclaw v$target_version..."
	fi

	# Install into OBOL_BIN_DIR using npm --prefix so the package lives
	# alongside the other managed binaries (works for both production
	# ~/.local/bin and development .workspace/bin layouts).
	local npm_prefix="$OBOL_BIN_DIR/.openclaw-npm"

	if npm install --prefix "$npm_prefix" "openclaw@$target_version" 2>&1; then
		# Create a wrapper script in OBOL_BIN_DIR that invokes the local install.
		# npm --prefix puts the .bin stubs in node_modules/.bin/ which handle
		# the correct entry point (openclaw.mjs) automatically.
		cat > "$OBOL_BIN_DIR/openclaw" <<WRAPPER
#!/usr/bin/env bash
exec "$npm_prefix/node_modules/.bin/openclaw" "\$@"
WRAPPER
		chmod +x "$OBOL_BIN_DIR/openclaw"
		log_success "openclaw v$target_version installed"
		return 0
	fi

	log_warn "Failed to install openclaw CLI"
	echo ""
	echo "  Install manually: npm install -g openclaw@$target_version"
	echo ""
	return 1
}

# Install Ollama (host runtime for local AI inference)
# Unlike other dependencies, Ollama is a full application with a background server.
# On macOS it installs Ollama.app; on Linux it sets up a systemd service.
# We delegate to Ollama's official installer rather than downloading a binary ourselves.
install_ollama() {
	# Check for existing ollama installation
	if command_exists ollama; then
		local version
		version=$(ollama --version 2>/dev/null | sed 's/ollama version is //' || echo "unknown")
		log_success "Ollama v$version already installed"

		# Check if the server is running
		if curl -sf http://localhost:11434/api/tags >/dev/null 2>&1; then
			log_success "Ollama server is running"
		else
			log_warn "Ollama is installed but the server is not running"
			echo ""
			case "$(uname -s)" in
			Darwin*)
				echo "  Start it with: open -a Ollama"
				;;
			Linux*)
				echo "  Start it with: ollama serve"
				;;
			esac
			echo ""
		fi
		return 0
	fi

	# Ollama not found — ask user if they want to install it
	echo ""
	log_info "Ollama is not installed"
	echo ""
	echo "  Ollama provides local AI inference for the Obol Stack."
	echo "  Without it, you can still use cloud providers (Anthropic, OpenAI)"
	echo "  via 'obol model setup'."
	echo ""

	# Check if we can prompt the user
	if [[ -c /dev/tty ]]; then
		local choice
		read -p "  Install Ollama now? [Y/n]: " choice </dev/tty

		case "$choice" in
		[Nn]*)
			log_warn "Skipping Ollama — local AI inference will be unavailable"
			echo ""
			echo "  Install later: curl -fsSL https://ollama.com/install.sh | sh"
			echo "  Then pull a model: obol model pull"
			echo ""
			return 0
			;;
		*)
			# Yes — delegate to official installer
			log_info "Installing Ollama..."
			echo ""
			if curl -fsSL https://ollama.com/install.sh | sh; then
				echo ""
				log_success "Ollama installed"

				# On macOS, the installer starts Ollama.app automatically.
				# On Linux with systemd, the installer enables and starts the service.
				# Give it a moment to come up.
				local attempts=0
				while [[ $attempts -lt 10 ]]; do
					if curl -sf http://localhost:11434/api/tags >/dev/null 2>&1; then
						log_success "Ollama server is running"
						echo ""
						echo "  Pull a model later with: obol model pull"
						echo ""
						return 0
					fi
					sleep 1
					attempts=$((attempts + 1))
				done

				log_warn "Ollama installed but server not yet responding"
				echo ""
				case "$(uname -s)" in
				Darwin*)
					echo "  Start it with: open -a Ollama"
					;;
				Linux*)
					echo "  Start it with: ollama serve"
					;;
				esac
				echo "  Then pull a model with: obol model pull"
				echo ""
				return 0
			else
				log_warn "Ollama installation failed"
				echo ""
				echo "  Install manually: https://ollama.com/download"
				echo ""
				return 1
			fi
			;;
		esac
	else
		# Non-interactive — just warn
		log_warn "Ollama not found — local AI inference will be unavailable"
		echo ""
		echo "  Install Ollama: curl -fsSL https://ollama.com/install.sh | sh"
		echo "  Then pull a model: obol model pull"
		echo ""
		return 0
	fi
}

# Install all dependencies
install_dependencies() {
	log_info "Checking and installing dependencies..."
	echo ""

	# Ensure OBOL_BIN_DIR is in PATH for the current process so that
	# binaries installed here (helm, helmfile, etc.) are immediately
	# available to each other and to later steps like `obol bootstrap`.
	if ! echo "$PATH" | grep -q "$OBOL_BIN_DIR"; then
		export PATH="$OBOL_BIN_DIR:$PATH"
	fi

	# Install each dependency
	install_kubectl || log_warn "kubectl installation failed (continuing...)"
	install_helm || log_warn "helm installation failed (continuing...)"
	if [[ "${OBOL_BACKEND:-k3d}" != "k3s" ]]; then
		install_k3d || log_warn "k3d installation failed (continuing...)"
	else
		log_info "Skipping k3d (using k3s backend)"
	fi
	install_helmfile || log_warn "helmfile installation failed (continuing...)"
	install_k9s || log_warn "k9s installation failed (continuing...)"
	install_helm_diff || log_warn "helm-diff plugin installation failed (continuing...)"
	install_openclaw || log_warn "openclaw CLI installation failed (continuing...)"
	install_ollama || log_warn "Ollama installation skipped (continuing...)"

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

	# Note: wildcard *.obol.stack DNS is handled by a local DNS resolver
	# that starts automatically with 'obol stack up'. The /etc/hosts entry
	# above provides baseline resolution for the root domain (obol.stack).
	log_info "Wildcard *.obol.stack DNS will be configured on first 'obol stack up'"
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
	echo "  Add this line to your shell profile ($profile_file):"
	echo ""
	echo "    export PATH=\"$OBOL_BIN_DIR:\$PATH\""
	echo ""
	echo "  Then reload your profile:"
	echo ""
	echo "    source $profile_file"
	echo ""
	log_dim "  Or export for current session only:"
	echo ""
	echo "    export PATH=\"$OBOL_BIN_DIR:\$PATH\""
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
	# Detect appropriate profile file
	local profile
	profile=$(detect_shell_profile)

	# Check if already persisted in the shell profile
	if [[ -f "$profile" ]] && grep -qF "$OBOL_BIN_DIR" "$profile" 2>/dev/null; then
		log_success "OBOL_BIN_DIR already configured in $profile"
		return 0
	fi

	# Check if we can prompt the user via /dev/tty (works even with curl | bash)
	if [[ -c /dev/tty ]]; then
		echo ""
		log_info "To use 'obol' command, $OBOL_BIN_DIR needs to be in your PATH"
		echo ""
		log_dim "  Detected shell profile: $profile"
		echo ""
		echo "  Options:"
		echo "    1. Automatically add to $profile (recommended)"
		echo "    2. Show manual instructions"
		echo ""

		local choice
		read -p "Choose [1/2]: " choice </dev/tty

		case "$choice" in
		1)
			add_to_profile "$profile"
			echo ""
			log_info "PATH updated for future sessions"
			echo ""
			log_dim "  To use immediately in this session, run:"
			echo ""
			echo "    export PATH=\"$OBOL_BIN_DIR:\$PATH\""
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
		# Truly non-interactive (CI/CD, no terminal): check environment variable override
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
# Check if ~/.openclaw/openclaw.json specifies a cloud model that needs an API key.
# If the key is missing and we have a TTY, prompt for it interactively and export
# it so the subsequent obol bootstrap / stack up picks it up via autoConfigureLLM.
check_agent_model_api_key() {
	local config_file="$HOME/.openclaw/openclaw.json"

	# Try to detect the model from an existing openclaw.json config.
	local primary_model=""
	if [[ -f "$config_file" ]] && command_exists python3; then
		primary_model=$(python3 -c "
import json, sys
try:
    d = json.load(open('$config_file'))
    print(d.get('agents',{}).get('defaults',{}).get('model',{}).get('primary',''))
except: pass
" 2>/dev/null)
	fi

	# Determine provider from config, or check for any pre-existing API keys
	# in the environment even on fresh installs.
	local provider="" env_var="" provider_name=""
	if [[ -n "$primary_model" ]]; then
		case "$primary_model" in
			*claude*) provider="anthropic"; env_var="ANTHROPIC_API_KEY"; provider_name="Anthropic" ;;
			gpt*|o1*|o3*|o4*) provider="openai"; env_var="OPENAI_API_KEY"; provider_name="OpenAI" ;;
		esac
	fi

	# If we identified a provider from config, check if the key is already set.
	if [[ -n "$provider" ]]; then
		echo ""
		if [[ -n "${!env_var:-}" ]]; then
			log_success "$env_var detected for $primary_model"
			return 0
		fi

		# Anthropic-specific fallback: Claude Code subscription token
		if [[ "$provider" == "anthropic" && -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
			export ANTHROPIC_API_KEY="$CLAUDE_CODE_OAUTH_TOKEN"
			log_success "Claude Code subscription detected (CLAUDE_CODE_OAUTH_TOKEN)"
			return 0
		fi

		# Interactive: prompt for the specific key
		if [[ -c /dev/tty ]]; then
			log_info "Your agent uses $primary_model ($provider_name)"
			echo ""
			local api_key=""
			read -r -p "  $provider_name API key ($env_var): " api_key </dev/tty
			if [[ -n "$api_key" ]]; then
				export "$env_var=$api_key"
				log_success "$env_var configured"
			else
				echo ""
				log_dim "  Skipped. Configure later: obol model setup"
			fi
		else
			log_warn "Agent uses $primary_model but $env_var is not set."
			log_dim "  Set it before starting: export $env_var=..."
			log_dim "  Or configure after startup: obol model setup"
		fi
		return 0
	fi

	# Fresh install (no openclaw.json) — check environment for known API keys
	# and offer to configure a cloud provider interactively.
	if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
		log_success "ANTHROPIC_API_KEY detected — will configure Anthropic in LiteLLM"
		return 0
	fi
	if [[ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]]; then
		export ANTHROPIC_API_KEY="$CLAUDE_CODE_OAUTH_TOKEN"
		log_success "Claude Code subscription detected (CLAUDE_CODE_OAUTH_TOKEN)"
		return 0
	fi
	if [[ -n "${OPENAI_API_KEY:-}" ]]; then
		log_success "OPENAI_API_KEY detected — will configure OpenAI in LiteLLM"
		return 0
	fi

	# No config file and no environment keys — prompt interactively
	if [[ -c /dev/tty ]]; then
		echo ""
		log_info "Cloud AI provider (optional — local Ollama models work without a key)"
		echo ""
		echo "  1. Anthropic (Claude)"
		echo "  2. OpenAI (GPT)"
		echo "  3. Skip (use local Ollama models only)"
		echo ""

		local choice
		read -p "Choose [1/2/3]: " choice </dev/tty

		case "$choice" in
		1)
			local api_key=""
			read -r -p "  Anthropic API key (ANTHROPIC_API_KEY): " api_key </dev/tty
			if [[ -n "$api_key" ]]; then
				export ANTHROPIC_API_KEY="$api_key"
				log_success "ANTHROPIC_API_KEY configured"
			else
				log_dim "  Skipped. Configure later: obol model setup"
			fi
			;;
		2)
			local api_key=""
			read -r -p "  OpenAI API key (OPENAI_API_KEY): " api_key </dev/tty
			if [[ -n "$api_key" ]]; then
				export OPENAI_API_KEY="$api_key"
				log_success "OPENAI_API_KEY configured"
			else
				log_dim "  Skipped. Configure later: obol model setup"
			fi
			;;
		*)
			log_dim "  Using local Ollama models. Configure a cloud provider later: obol model setup"
			;;
		esac
	else
		log_dim "  No cloud AI provider API key found in environment."
		log_dim "  The agent will use local Ollama models."
		log_dim "  Configure a cloud provider after startup: obol model setup"
	fi
}

print_instructions() {
	local install_mode="$1"

	echo ""
	if [[ "$install_mode" == "upgrade" ]]; then
		log_success "Obol Stack upgrade complete!"
	else
		log_success "Obol Stack installation complete!"
	fi
	echo ""

	# Check if the agent's primary model requires a cloud API key.
	check_agent_model_api_key

	# Check if we can prompt the user for bootstrap (works with curl | bash via /dev/tty)
	if [[ -c /dev/tty ]] && [[ -f "$OBOL_BIN_DIR/obol" ]]; then
		echo ""
		log_info "Would you like to start the cluster now?"
		echo ""
		echo "  This will:"
		echo "    • Initialize cluster configuration"
		echo "    • Start the Obol Stack"
		echo "    • Open your browser to http://obol.stack"
		echo ""

		local choice
		read -p "Start cluster now? [y/N]: " choice </dev/tty

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
				echo "    obol stack init"
				echo "    obol stack up"
				echo "    obol agent init"
				echo ""
				return 1
			fi
			;;
		*)
			# User declined, show manual instructions
			echo ""
			log_info "To start the cluster later, run:"
			echo ""
			echo "    obol stack init"
			echo "    obol stack up"
			echo ""
			log_dim "  Then open your browser to: http://obol.stack"
			echo ""
			;;
		esac
	else
		# Truly non-interactive (CI/CD, no terminal) or no binary - show manual instructions
		log_dim "  Verify installation:"
		echo ""
		echo "    obol version"
		echo ""
		echo "  To start the cluster, run:"
		echo ""
		echo "    obol stack init"
		echo "    obol stack up"
		echo ""
		log_dim "  Then open your browser to: http://obol.stack"
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
	echo -e "${OBOL_GREEN}${BOLD}"
	echo "   ██████╗ ██████╗  ██████╗ ██╗         ███████╗████████╗ █████╗  ██████╗██╗  ██╗"
	echo "  ██╔═══██╗██╔══██╗██╔═══██╗██║         ██╔════╝╚══██╔══╝██╔══██╗██╔════╝██║ ██╔╝"
	echo "  ██║   ██║██████╔╝██║   ██║██║         ███████╗   ██║   ███████║██║     █████╔╝"
	echo "  ██║   ██║██╔══██╗██║   ██║██║         ╚════██║   ██║   ██╔══██║██║     ██╔═██╗"
	echo "  ╚██████╔╝██████╔╝╚██████╔╝███████╗    ███████║   ██║   ██║  ██║╚██████╗██║  ██╗"
	echo "   ╚═════╝ ╚═════╝  ╚═════╝ ╚══════╝    ╚══════╝   ╚═╝   ╚═╝  ╚═╝ ╚═════╝╚═╝  ╚═╝"
	echo -e "${NC}"
	echo -e "   ${OBOL_MUTED}Decentralised infrastructure for AI agents${NC}"
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

	# Check Docker prerequisites (only required for k3d backend, not k3s)
	if [[ "${OBOL_BACKEND:-k3d}" != "k3s" ]]; then
		if ! check_docker; then
			log_error "Docker requirements not met"
			echo ""
			log_dim "  If you want to use the k3s backend (no Docker required), run:"
			echo "    OBOL_BACKEND=k3s $0"
			exit 1
		fi
	else
		log_info "Backend: k3s (Docker not required)"
	fi
	echo ""

	# Check host prerequisites (npm/Node.js, etc.) before starting installs
	if ! check_prerequisites; then
		exit 1
	fi

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