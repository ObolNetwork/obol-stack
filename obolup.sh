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

# Pinned host tool versions.
# obolup.sh does not download these tools itself: after the obol binary is
# installed, `obol upgrade --tools-only` installs them from
# internal/tools/manifest.yaml, which pins the same versions together with a
# per-platform sha256 (verification fails closed). The values here are kept
# for Renovate and for the summary printed before installation;
# TestManifestMatchesObolup keeps them equal to manifest.yaml, so a version
# bump here must be paired with the manifest version AND sha256 update.
# renovate: datasource=github-releases depName=kubernetes/kubernetes
readonly KUBECTL_VERSION="1.37.1"
# renovate: datasource=github-releases depName=helm/helm
readonly HELM_VERSION="4.3.0"
# renovate: datasource=github-releases depName=k3d-io/k3d
readonly K3D_VERSION="5.9.0"
# renovate: datasource=github-releases depName=helmfile/helmfile
readonly HELMFILE_VERSION="1.8.1"
# renovate: datasource=github-releases depName=derailed/k9s
readonly K9S_VERSION="0.51.0"
# renovate: datasource=github-releases depName=databus23/helm-diff
readonly HELM_DIFF_VERSION="3.15.15"
# renovate: datasource=github-releases depName=ollama/ollama
readonly OLLAMA_VERSION="0.40.4"

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

# Non-interactive install mode — set OBOL_ASSUME_YES=1 (or pass --yes/-y) to
# accept every prompt. Useful for CI, remote installs, and `curl | sh` runs
# where /dev/tty is unavailable.
ASSUME_YES=false
if [[ "${OBOL_ASSUME_YES:-}" == "1" || "${OBOL_ASSUME_YES:-}" == "true" ]]; then
	ASSUME_YES=true
fi
for arg in "$@"; do
	case "$arg" in
	--yes | -y) ASSUME_YES=true ;;
	esac
done
readonly ASSUME_YES

# OBOL_NONINTERACTIVE=true never prompts: every question takes its safe
# default (no for anything that installs extra software or starts/stops
# services) and the installer prints the command to run later instead. The
# same applies automatically when no terminal is available (CI, pipes without
# a controlling terminal). --yes still accepts prompts explicitly.
NONINTERACTIVE=false
if [[ "${OBOL_NONINTERACTIVE:-}" == "1" || "${OBOL_NONINTERACTIVE:-}" == "true" ]]; then
	NONINTERACTIVE=true
fi
readonly NONINTERACTIVE

# Check if command exists
command_exists() {
	command -v "$1" >/dev/null 2>&1
}

# can_prompt succeeds when the user can be asked a question. Prompts read from
# /dev/tty rather than stdin, because under `curl ... | bash` stdin is the
# script itself. `[[ -c /dev/tty ]]` only checks the device node exists; in
# containers and some CI runners it exists but cannot be opened (no
# controlling terminal), so also check that it actually opens.
can_prompt() {
	[[ "$NONINTERACTIVE" != "true" ]] || return 1
	[[ -c /dev/tty ]] || return 1
	{ true </dev/tty; } 2>/dev/null
}

# prompt_read <var> <prompt> <default>
# The only place obolup.sh reads user input. Stores the answer (or <default>
# when the user just presses enter, input ends, or prompting is impossible)
# in the variable named <var>. Never blocks without a terminal.
prompt_read() {
	local __var="$1" __prompt="$2" __default="$3" __reply=""
	if can_prompt; then
		if ! read -r -p "$__prompt" __reply </dev/tty 2>/dev/tty; then
			__reply=""
		fi
	fi
	if [[ -z "$__reply" ]]; then
		__reply="$__default"
	fi
	printf -v "$__var" '%s' "$__reply"
}

# confirm <question> <default y|n>
# Returns 0 for yes. --yes answers yes; without a terminal (or with
# OBOL_NONINTERACTIVE=true) the default is used, so callers must pass "n" for
# anything that installs extra software or starts/stops services.
confirm() {
	local question="$1" default="$2" hint="[y/N]" answer=""
	if [[ "$default" == "y" ]]; then
		hint="[Y/n]"
	fi
	if [[ "$ASSUME_YES" == "true" ]]; then
		log_info "$question — yes (--yes)"
		return 0
	fi
	if ! can_prompt; then
		return 1
	fi
	prompt_read answer "  $question $hint: " "$default"
	[[ "$answer" == [Yy]* ]]
}

# sudo_available succeeds when a sudo command can run without hanging:
# either credentials are cached / NOPASSWD (`sudo -n true`), or a terminal is
# available for the password prompt. Without a terminal, sudo would fail or
# wait for a password nobody can type, so callers skip the step and print
# the command for the user to run instead.
sudo_available() {
	if [[ "$(id -u)" == "0" ]]; then
		return 0
	fi
	command_exists sudo || return 1
	if sudo -n true 2>/dev/null; then
		return 0
	fi
	can_prompt
}

# sudo_cmd runs a command as root (directly when already root).
sudo_cmd() {
	if [[ "$(id -u)" == "0" ]]; then
		"$@"
	else
		sudo "$@"
	fi
}

# run_obol runs the installed obol binary against the directories this script
# installed into (they may come from XDG defaults or the dev .workspace and
# are not necessarily exported).
run_obol() {
	env OBOL_CONFIG_DIR="$OBOL_CONFIG_DIR" \
		OBOL_DATA_DIR="$OBOL_DATA_DIR" \
		OBOL_STATE_DIR="$OBOL_STATE_DIR" \
		OBOL_BIN_DIR="$OBOL_BIN_DIR" \
		"$OBOL_BIN_DIR/obol" "$@"
}

# Check host prerequisites that the installer cannot provide itself.
# Binaries we download (helm, kubectl, k3d, etc.) are not checked here —
# only tools the user must install separately.
check_prerequisites() {
	local missing=()

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

docker_info_check() {
	local seconds="${1:-15}"
	local output_file="${2:-${TMPDIR:-/tmp}/obol-docker-info.log}"

	rm -f "$output_file"
	docker info >"$output_file" 2>&1 &
	local pid=$!
	for _ in $(seq 1 "$seconds"); do
		if ! kill -0 "$pid" 2>/dev/null; then
			if wait "$pid"; then
				return 0
			else
				local rc=$?
				return "$rc"
			fi
		fi
		sleep 1
	done

	kill "$pid" >/dev/null 2>&1 || true
	wait "$pid" >/dev/null 2>&1 || true
	return 124
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
	local docker_check_log="${TMPDIR:-/tmp}/obol-docker-info.$$"
	if ! docker_info_check 15 "$docker_check_log"; then
		# Distinguish permission errors from daemon-not-running
		local docker_err
		docker_err=$(cat "$docker_check_log" 2>/dev/null || true)

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
			# Starting the daemon needs root. Only try when sudo cannot
			# hang (cached credentials, NOPASSWD, or a terminal for the
			# password prompt); otherwise fall through to the instructions.
			if sudo_available; then
				log_warn "Docker daemon is not running — attempting to start..."
				# Try systemd first (apt/yum installs), then snap
				if command_exists systemctl && systemctl list-unit-files docker.service >/dev/null 2>&1; then
					sudo_cmd systemctl start docker 2>/dev/null && sleep 2
				elif command_exists snap && snap list docker >/dev/null 2>&1; then
					sudo_cmd snap start docker 2>/dev/null && sleep 3
				fi
			else
				log_warn "Docker daemon is not running and sudo needs a password, which cannot be entered without a terminal"
			fi
		elif [[ "$(uname -s)" == "Darwin" ]]; then
			# Start Docker Desktop if it's installed but not running
			if [[ -d "/Applications/Docker.app" ]]; then
				log_warn "Docker Desktop is not running — starting it now..."
				if ! open -a Docker; then
					log_warn "Could not open Docker Desktop"
				fi
				# Docker Desktop can take a while to initialise; poll until ready
				local wait_secs=0
				while ! docker_info_check 10 "$docker_check_log"; do
					if [[ $wait_secs -ge 60 ]]; then
						break
					fi
					sleep 2
					wait_secs=$((wait_secs + 2))
				done
			fi
		fi

		# Re-check after start attempt
		if ! docker_info_check 15 "$docker_check_log"; then
			log_error "Docker daemon is not running"
			echo ""
			echo "  Please start the Docker daemon:"
			if [[ "$(uname -s)" == "Linux" ]]; then
				echo "    • systemd:  sudo systemctl start docker"
				echo "    • snap:     sudo snap start docker"
			else
				echo "    • macOS/Windows: Start Docker Desktop application"
				echo "    • If macOS says Docker is damaged, move /Applications/Docker.app to Trash"
				echo "      and reinstall Docker Desktop from https://www.docker.com/products/docker-desktop/"
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

	local tmp_obol="$OBOL_BIN_DIR/obol.tmp"

	# Create wrapper script that uses 'go run'
	cat >"$tmp_obol" <<'EOF'
#!/usr/bin/env bash
# Obol CLI Development Wrapper
# This script runs the obol CLI using 'go run' for rapid development

# Find the project root (where obolup.sh is located)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Run the CLI
cd "$SCRIPT_DIR" && exec go run -a ./cmd/obol "$@"
EOF

	chmod +x "$tmp_obol"
	mv "$tmp_obol" "$OBOL_BIN_DIR/obol"
	log_success "Installed development wrapper at $OBOL_BIN_DIR/obol"
}

# Compute the SHA256 hash of a file.
# Portable: macOS ships shasum, Linux ships sha256sum.
sha256_file() {
	local file="$1"
	if command_exists sha256sum; then
		sha256sum "$file" | awk '{print $1}'
	elif command_exists shasum; then
		shasum -a 256 "$file" | awk '{print $1}'
	else
		return 1
	fi
}

# Download a URL to a file with retries (3 attempts, short sleep between).
# Used for the obol release binary and its SHA256SUMS file only.
download_with_retries() {
	local url="$1"
	local dest="$2"
	local attempts=3
	local attempt

	for attempt in $(seq 1 "$attempts"); do
		if command_exists curl; then
			if curl -fsSL "$url" -o "$dest"; then
				return 0
			fi
		elif command_exists wget; then
			if wget -q "$url" -O "$dest"; then
				return 0
			fi
		else
			log_error "Neither curl nor wget is available"
			return 1
		fi

		if [[ "$attempt" -lt "$attempts" ]]; then
			log_warn "Download failed (attempt $attempt/$attempts), retrying in 2s..."
			sleep 2
		fi
	done

	rm -f "$dest"
	return 1
}

# Verify a downloaded release binary against the published SHA256SUMS file.
# Fails closed: returns 2 on a *verified* checksum mismatch (tamper — the caller
# must abort hard and NOT fall back to a source build), and returns 1 on a
# missing checksum entry or no local sha256 tool. If SHA256SUMS itself can't be
# fetched, also fails closed unless OBOL_ALLOW_UNVERIFIED=1 is explicitly set,
# in which case the install proceeds unverified (return 0).
verify_release_checksum() {
	local release_tag="$1"
	local binary_name="$2"
	local binary_path="$3"

	local sums_url="https://github.com/ObolNetwork/obol-stack/releases/download/${release_tag}/SHA256SUMS"
	local tmp_sums="${binary_path}.sha256sums"

	if ! download_with_retries "$sums_url" "$tmp_sums"; then
		if [[ "${OBOL_ALLOW_UNVERIFIED:-}" == "1" || "${OBOL_ALLOW_UNVERIFIED:-}" == "true" ]]; then
			log_warn "SHA256SUMS not published for $release_tag, skipping checksum verification (OBOL_ALLOW_UNVERIFIED set)"
			return 0
		fi
		log_error "Could not fetch SHA256SUMS for $release_tag, refusing to install an unverified binary"
		echo ""
		echo "  Set OBOL_ALLOW_UNVERIFIED=1 to bypass this check if you understand the risk."
		echo ""
		return 1
	fi

	# Match on the artifact name as published (e.g. obol_darwin_arm64),
	# handling both "hash  name" and "hash *name" (binary mode) formats.
	local expected_hash
	expected_hash=$(awk -v name="$binary_name" '$2 == name || $2 == "*"name {print $1; exit}' "$tmp_sums")
	rm -f "$tmp_sums"

	if [[ -z "$expected_hash" ]]; then
		log_error "No checksum entry for $binary_name in SHA256SUMS ($release_tag), refusing to install an unverified binary"
		return 1
	fi

	local actual_hash
	if ! actual_hash=$(sha256_file "$binary_path"); then
		log_error "Neither sha256sum nor shasum is available, refusing to install an unverified binary"
		return 1
	fi

	if [[ "$actual_hash" != "$expected_hash" ]]; then
		log_error "Checksum mismatch for $binary_name ($release_tag)"
		echo ""
		echo "  Expected: $expected_hash"
		echo "  Actual:   $actual_hash"
		echo ""
		echo "The downloaded binary may be corrupted or tampered with."
		echo "Please re-run the installer. If the problem persists, report an issue at:"
		echo "  https://github.com/ObolNetwork/obol-stack/issues"
		echo ""
		# Exit code 2 signals a *verified* mismatch (as opposed to the
		# generic 1 used above for "couldn't verify at all") so callers can
		# tell tamper apart from unavailability and refuse to silently fall
		# back to another download over the same (possibly compromised)
		# channel.
		return 2
	fi

	log_success "Checksum verified for $binary_name"
	return 0
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

	local tmp_obol="$OBOL_BIN_DIR/obol.tmp"

	# Download binary (with retries)
	if ! download_with_retries "$download_url" "$tmp_obol"; then
		log_warn "Failed to download release $release_tag"
		rm -f "$tmp_obol"
		return 1
	fi

	# Verify against the published SHA256SUMS before installing. Propagate
	# a verified-tamper (exit 2) distinctly from a generic verification
	# failure (exit 1) so the caller knows falling back to another download
	# method is unsafe.
	local verify_status=0
	verify_release_checksum "$release_tag" "$binary_name" "$tmp_obol" || verify_status=$?
	if [[ "$verify_status" -ne 0 ]]; then
		rm -f "$tmp_obol"
		return "$verify_status"
	fi

	chmod +x "$tmp_obol"
	mv "$tmp_obol" "$OBOL_BIN_DIR/obol"
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

	local tmp_obol="$OBOL_BIN_DIR/obol.tmp"

	if ! go build -ldflags "$ldflags" -o "$tmp_obol" ./cmd/obol; then
		log_error "Failed to build binary"
		rm -f "$tmp_obol"
		return 1
	fi

	chmod +x "$tmp_obol"
	mv "$tmp_obol" "$OBOL_BIN_DIR/obol"
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
			local dl_status=0
			download_release "$latest_tag" || dl_status=$?
			if [[ "$dl_status" -eq 0 ]]; then
				show_version_change "$current_version"
				return 0
			fi
			if [[ "$dl_status" -eq 2 ]]; then
				# Verified tamper (checksum mismatch), not mere
				# unavailability: building from source would just re-fetch
				# over the same, possibly compromised, channel. Abort hard
				# instead of silently "recovering" into an unverified install.
				log_error "Checksum verification failed for $latest_tag — refusing to fall back to building from source over the same network channel."
				return 1
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
		local dl_status=0
		download_release "$release" || dl_status=$?
		if [[ "$dl_status" -eq 0 ]]; then
			show_version_change "$current_version"
			return 0
		fi
		if [[ "$dl_status" -eq 2 ]]; then
			log_error "Checksum verification failed for $release — refusing to fall back to building from source over the same network channel."
			return 1
		fi

		log_warn "Release $release not found or download failed, building from source..."
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
			echo "  curl -fsSL $script_source_url -o $OBOL_BIN_DIR/obolup.sh"
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


# Install the pinned host tools (kubectl, helm, k3d, helmfile, k9s and the
# helm-diff plugin) through the obol CLI. `obol upgrade --tools-only`
# downloads the versions pinned in internal/tools/manifest.yaml, verifies each
# download against its sha256 (fail closed) and installs it atomically into
# the obol bin dir. Compatible copies already on $PATH (or set via
# OBOL_<TOOL>) are used as they are and never modified.
install_tools() {
	log_info "Installing pinned tools..."
	log_dim "  kubectl v$KUBECTL_VERSION, helm v$HELM_VERSION, k3d v$K3D_VERSION, helmfile v$HELMFILE_VERSION, k9s v$K9S_VERSION, helm-diff v$HELM_DIFF_VERSION (sha256-verified)"

	if [[ ! -x "$OBOL_BIN_DIR/obol" ]]; then
		log_warn "obol binary not found in $OBOL_BIN_DIR, cannot install tools"
		return 1
	fi

	if [[ "${OBOL_DEVELOPMENT:-false}" == "true" ]]; then
		log_dim "  Development mode: the first obol invocation compiles the CLI with 'go run', this can take a while"
	fi

	if run_obol upgrade --tools-only; then
		return 0
	fi

	log_warn "Tool installation did not complete (continuing...)"
	echo ""
	echo "  Retry later with:"
	echo "    obol upgrade --tools-only"
	echo ""
	log_dim "  'obol stack init' and 'obol stack up' also install missing required tools."
	echo ""
	return 1
}

readonly OLLAMA_INSTALL_URL="https://ollama.com/install.sh"

# Describe what Ollama's official installer does before asking to run it.
describe_ollama_installer() {
	echo "  This runs Ollama's official installer ($OLLAMA_INSTALL_URL), which:"
	case "$(uname -s)" in
	Darwin*)
		echo "    • installs the Ollama app and the ollama CLI"
		echo "    • starts the Ollama server"
		;;
	Linux*)
		echo "    • installs ollama under /usr/local (needs sudo)"
		echo "    • creates an 'ollama' system user and a systemd service, and starts it"
		echo "    • may install GPU driver packages through your package manager if it"
		echo "      detects a GPU without drivers"
		echo "  On systemd hosts obolup.sh then offers to make Ollama listen on 0.0.0.0"
		echo "  so the cluster can reach it."
		;;
	esac
	log_dim "  Review the script first with: curl -fsSL $OLLAMA_INSTALL_URL | less"
}

# Print how to install or upgrade Ollama later.
print_ollama_later() {
	echo ""
	echo "  Install it later with either of:"
	echo "    curl -fsSL $OLLAMA_INSTALL_URL | OLLAMA_VERSION=$OLLAMA_VERSION sh"
	echo "    OBOL_INSTALL_OLLAMA=true obolup.sh"
	echo "  Then pull a model: obol model pull"
	echo ""
}

# ollama_consent <question>
# Ollama is optional, so it is installed or upgraded only on explicit consent:
# OBOL_INSTALL_OLLAMA=true, or "y" at a terminal prompt (default No).
# --yes does not imply it, and without a terminal the answer is No.
ollama_consent() {
	local question="$1" choice=""
	case "${OBOL_INSTALL_OLLAMA:-}" in
	1 | true | yes)
		log_info "OBOL_INSTALL_OLLAMA set — $question yes"
		return 0
		;;
	0 | false | no)
		return 1
		;;
	esac
	if [[ "$ASSUME_YES" == "true" ]]; then
		log_dim "  --yes does not install Ollama; set OBOL_INSTALL_OLLAMA=true to include it"
		return 1
	fi
	if ! can_prompt; then
		log_dim "  Non-interactive: not installing Ollama (set OBOL_INSTALL_OLLAMA=true to include it)"
		return 1
	fi
	prompt_read choice "  $question [y/N]: " "n"
	[[ "$choice" == [Yy]* ]]
}

# Run Ollama's official installer for the pinned version.
run_ollama_installer() {
	if [[ "$(uname -s)" == "Linux" ]] && ! sudo_available; then
		log_warn "Ollama's installer needs sudo, which would prompt for a password without a terminal — skipping"
		print_ollama_later
		return 1
	fi
	env OLLAMA_VERSION="$OLLAMA_VERSION" bash -c "curl -fsSL $OLLAMA_INSTALL_URL | sh"
}

# Configure Ollama to listen on 0.0.0.0 so k3d containers can reach it.
# On macOS, Docker Desktop routes host.docker.internal through the hypervisor,
# so Ollama's bind address doesn't matter. On Linux, k3d uses the docker
# bridge network, so Ollama must accept connections from the bridge gateway IP.
configure_ollama_host_binding() {
	# Only needed on Linux with systemd
	[[ "$(uname -s)" != "Linux" ]] && return 0
	command_exists systemctl || return 0
	systemctl list-unit-files ollama.service >/dev/null 2>&1 || return 0

	local override_dir="/etc/systemd/system/ollama.service.d"
	local override_file="$override_dir/obol-host.conf"

	# Skip if already configured
	if [[ -f "$override_file" ]]; then
		return 0
	fi

	echo ""
	log_info "Ollama must listen on 0.0.0.0 for the k3d cluster to reach it"
	echo "  This writes $override_file, which makes Ollama listen on all"
	echo "  network interfaces, and restarts the ollama service if it is running."

	local can_configure=false
	if ! sudo_available; then
		log_warn "Skipping: sudo needs a password and there is no terminal to enter it"
	elif confirm "Configure and restart Ollama now?" "y"; then
		can_configure=true
	else
		log_warn "Skipping Ollama host binding"
	fi

	if [[ "$can_configure" != "true" ]]; then
		echo ""
		echo "  To do it yourself, run:"
		echo "    sudo mkdir -p $override_dir"
		printf '    %s\n' "printf '[Service]\\nEnvironment=\"OLLAMA_HOST=0.0.0.0\"\\n' | sudo tee $override_file"
		echo "    sudo systemctl daemon-reload && sudo systemctl restart ollama"
		echo ""
		return 0
	fi

	if sudo_cmd mkdir -p "$override_dir" &&
		sudo_cmd tee "$override_file" >/dev/null <<'OVERRIDE'
[Service]
Environment="OLLAMA_HOST=0.0.0.0"
OVERRIDE
	then
		sudo_cmd systemctl daemon-reload
		if systemctl is-active --quiet ollama; then
			log_warn "Restarting Ollama service to apply new bind address..."
			sudo_cmd systemctl restart ollama 2>/dev/null || true
		fi
		log_success "Ollama configured to listen on all interfaces"
	else
		log_warn "Could not configure Ollama host binding (non-fatal)"
		echo "  Manually set OLLAMA_HOST=0.0.0.0 in your Ollama config"
	fi
}

# Check whether the Ollama server answers and print how to start it if not.
check_ollama_server() {
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
}

# Install or upgrade Ollama (host runtime for local AI inference).
# Unlike the pinned tools, Ollama is a full application with a background
# server (Ollama.app on macOS, a systemd service on Linux), so it is optional
# and only installed with explicit consent (see ollama_consent). We delegate
# to Ollama's official installer rather than downloading a binary ourselves.
install_ollama() {
	local target_version="$OLLAMA_VERSION"

	# Check for existing ollama installation
	if command_exists ollama; then
		local version
		version=$(ollama --version 2>/dev/null | sed 's/ollama version is //' || echo "unknown")

		if [[ -z "$version" || "$version" == "unknown" ]]; then
			log_success "Ollama already installed (version unknown)"
			check_ollama_server
			return 0
		fi

		if version_ge "$version" "$target_version"; then
			log_success "Ollama v$version is up to date"
			configure_ollama_host_binding
			check_ollama_server
			return 0
		fi

		log_warn "Ollama v$version is older than pinned v$target_version"
		describe_ollama_installer
		if ! ollama_consent "Upgrade Ollama to v$target_version?"; then
			log_warn "Skipping Ollama upgrade"
			echo "  Upgrade later with: curl -fsSL $OLLAMA_INSTALL_URL | OLLAMA_VERSION=$target_version sh"
			check_ollama_server
			return 0
		fi

		log_info "Upgrading Ollama to v$target_version..."
		echo ""
		if run_ollama_installer; then
			echo ""
			log_success "Ollama upgraded to v$target_version"
			configure_ollama_host_binding
		else
			log_warn "Ollama upgrade did not complete — continuing with v$version"
		fi
		check_ollama_server
		return 0
	fi

	# Ollama not found — ask the user whether to install it
	echo ""
	log_info "Ollama is not installed (optional)"
	echo ""
	echo "  Ollama provides local AI inference for the Obol Stack."
	echo "  Without it, you can still use cloud providers (Anthropic, OpenAI)"
	echo "  via 'obol model setup'."
	echo ""
	describe_ollama_installer
	echo ""

	if ! ollama_consent "Install Ollama v$target_version now?"; then
		log_warn "Skipping Ollama — local AI inference will be unavailable"
		print_ollama_later
		return 0
	fi

	log_info "Installing Ollama v$target_version..."
	echo ""
	if ! run_ollama_installer; then
		log_warn "Ollama installation did not complete"
		echo ""
		echo "  Install manually: https://ollama.com/download"
		echo ""
		return 1
	fi

	echo ""
	log_success "Ollama v$target_version installed"
	configure_ollama_host_binding

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

	install_tools || true
	install_ollama || log_warn "Ollama installation skipped (continuing...)"

	echo ""
	log_success "Dependencies check complete"
}

# Check if obol.stack hostname is configured in /etc/hosts
check_hosts_file() {
	log_info "Checking /etc/hosts for obol.stack entry..."

	# Check if /etc/hosts contains obol.stack (exact hostname, not e.g. dev.obol.stack)
	if grep -E "^[^#]*[[:space:]]obol\.stack([[:space:]]|$)" /etc/hosts >/dev/null 2>&1; then
		# Check if it points to localhost (127.0.0.1 or ::1)
		if grep -E "^(127\.0\.0\.1|::1)[[:space:]]([^#]*[[:space:]])?obol\.stack([[:space:]]|$)" /etc/hosts >/dev/null 2>&1; then
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

# Print the manual /etc/hosts instructions.
print_hosts_instructions() {
	local hosts_entry="$1"
	echo ""
	echo "Please add this line to /etc/hosts yourself:"
	echo ""
	echo "  $hosts_entry"
	echo ""
	echo "For example:"
	echo "  echo '$hosts_entry' | sudo tee -a /etc/hosts"
	echo ""
}

# Add obol.stack entry to /etc/hosts
update_hosts_file() {
	log_info "Adding obol.stack to /etc/hosts..."

	local hosts_entry="127.0.0.1 obol.stack"

	# Idempotency guard: skip the append if a localhost entry already exists
	# (protects against duplicates on re-run, even if the caller's check changes)
	if grep -E "^(127\.0\.0\.1|::1)[[:space:]]([^#]*[[:space:]])?obol\.stack([[:space:]]|$)" /etc/hosts >/dev/null 2>&1; then
		log_success "obol.stack already configured in /etc/hosts, skipping"
		return 0
	fi

	if [[ "$(id -u)" != "0" ]] && ! command_exists sudo; then
		log_warn "sudo not available, cannot update /etc/hosts automatically"
		print_hosts_instructions "$hosts_entry"
		return 1
	fi

	# Never let sudo wait for a password nobody can type.
	if ! sudo_available; then
		log_warn "Skipping /etc/hosts update: sudo needs a password and there is no terminal to enter it"
		print_hosts_instructions "$hosts_entry"
		return 1
	fi

	if [[ "$(id -u)" == "0" ]] || sudo -n true 2>/dev/null; then
		# Already root, cached credentials, or NOPASSWD configured
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
	if echo "$hosts_entry" | sudo_cmd tee -a /etc/hosts >/dev/null 2>&1; then
		log_success "Added obol.stack to /etc/hosts"
		return 0
	fi

	log_warn "Failed to update /etc/hosts"
	print_hosts_instructions "$hosts_entry"
	return 1
}

# Check and configure /etc/hosts entry for obol.stack
configure_hosts_file() {
	if ! check_hosts_file; then
		# Non-fatal: the stack works without it, and `obol stack up` retries.
		update_hosts_file || true
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

	# Check if already on the current PATH (e.g. set by a prior run or parent
	# shell). No point prompting if the binary is already reachable.
	if echo "$PATH" | grep -qF "$OBOL_BIN_DIR"; then
		log_success "$OBOL_BIN_DIR is already on PATH"
		return 0
	fi

	# Prompt via /dev/tty when possible (works even with curl | bash).
	# --yes auto-picks option 1 (add to profile).
	if [[ "$ASSUME_YES" == "true" ]] || can_prompt; then
		echo ""
		log_info "To use 'obol' command, $OBOL_BIN_DIR needs to be in your PATH"
		echo ""
		log_dim "  Detected shell profile: $profile"
		echo ""
		echo "  Options:"
		echo "    1. Automatically add to $profile (recommended)"
		echo "    2. Show manual instructions"
		echo ""

		local choice=""
		if [[ "$ASSUME_YES" == "true" ]]; then
			choice="1"
			log_info "--yes set — adding $OBOL_BIN_DIR to $profile"
		else
			prompt_read choice "Choose [1/2]: " "2"
		fi

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
		*)
			print_path_instructions "$profile"
			;;
		esac
	else
		# Non-interactive (CI/CD, no terminal, OBOL_NONINTERACTIVE=true):
		# only modify the profile when explicitly asked.
		if [[ "${OBOL_MODIFY_PATH:-no}" == "yes" ]]; then
			add_to_profile "$profile"
			log_info "Will be available in new shell sessions"
		else
			print_path_instructions "$profile"
		fi
	fi
}

# stack_state prints the state of the stack configured in OBOL_CONFIG_DIR:
#   none     no stack initialised (no .stack-id)
#   running  initialised and the cluster API answers
#   stopped  initialised but the cluster API does not answer
stack_state() {
	if [[ ! -f "$OBOL_CONFIG_DIR/.stack-id" ]]; then
		echo "none"
		return 0
	fi

	local kubeconfig="$OBOL_CONFIG_DIR/kubeconfig.yaml"
	local kubectl_bin="${OBOL_KUBECTL:-}"
	if [[ -z "$kubectl_bin" && -x "$OBOL_BIN_DIR/kubectl" ]]; then
		kubectl_bin="$OBOL_BIN_DIR/kubectl"
	fi
	if [[ -z "$kubectl_bin" ]]; then
		kubectl_bin=$(command -v kubectl 2>/dev/null || true)
	fi

	if [[ -f "$kubeconfig" && -n "$kubectl_bin" ]] &&
		"$kubectl_bin" --kubeconfig "$kubeconfig" --request-timeout=5s get --raw=/readyz >/dev/null 2>&1; then
		echo "running"
	else
		echo "stopped"
	fi
}

# upgrade_running_stack offers to bring an already-running stack up to the
# obol version just installed (`obol stack up` applies the new defaults,
# charts and migrations). It restarts services, so it is a question, default
# yes, on a terminal; without one it only runs with --yes or
# OBOL_UPGRADE_STACK=true and otherwise prints the command.
upgrade_running_stack() {
	local installed stack_version
	installed=$("$OBOL_BIN_DIR/obol" version 2>/dev/null | awk '/^Version:/ {print $2; exit}')
	# Written by `obol stack up` since 0.17.1; absent for older stacks.
	stack_version=$(tr -d '[:space:]' <"$OBOL_CONFIG_DIR/.stack-version" 2>/dev/null || true)

	log_success "Your Obol Stack is running"
	if [[ -n "$installed" && "$stack_version" == "$installed" ]]; then
		echo ""
		echo "  It is already on obol $installed."
		echo "  Check the stack's health:  obol doctor"
		echo ""
		return 0
	fi

	echo ""
	echo "  The stack was last brought up by ${stack_version:+obol }${stack_version:-an earlier obol release}; you installed obol ${installed:-a new version}."
	echo "  Upgrading it restarts LiteLLM and the x402 services (about a minute; paid requests in flight will fail)."
	echo ""

	local upgrade=false
	if [[ "${OBOL_UPGRADE_STACK:-}" == "1" || "${OBOL_UPGRADE_STACK:-}" == "true" ]]; then
		upgrade=true
	elif confirm "Upgrade the running stack now (obol stack up)?" "y"; then
		upgrade=true
	fi

	if [[ "$upgrade" != "true" ]]; then
		log_dim "  Not upgrading the stack now. Finish the update later with:"
		echo ""
		echo "    obol stack up"
		echo ""
		return 0
	fi

	echo ""
	if run_obol stack up; then
		return 0
	fi
	log_error "obol stack up failed"
	echo ""
	log_info "Check what is wrong with: obol doctor"
	log_info "Then retry with:          obol stack up"
	echo ""
	return 1
}

# Print the manual "start the stack" instructions.
print_start_instructions() {
	echo "  To start the cluster, run:"
	echo ""
	echo "    obol stack init"
	echo "    obol stack up"
	echo ""
	log_dim "  Then open your browser to: http://obol.stack"
	echo ""
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

	if [[ ! -x "$OBOL_BIN_DIR/obol" ]]; then
		log_dim "  Verify installation:"
		echo ""
		echo "    obol version"
		echo ""
		print_start_instructions
		return 0
	fi

	local state
	state=$(stack_state)

	case "$state" in
	running)
		upgrade_running_stack
		return $?
		;;
	stopped)
		log_info "An Obol Stack is configured but not running"
		echo ""
		if confirm "Start it now (obol stack up)?" "y"; then
			echo ""
			if run_obol stack up; then
				return 0
			fi
			log_error "obol stack up failed"
			echo ""
			log_info "Check what is wrong with: obol doctor"
			log_info "Then retry with:          obol stack up"
			echo ""
			return 1
		fi
		log_dim "  Not starting the stack now. Start it later with:"
		echo ""
		echo "    obol stack up"
		echo ""
		return 0
		;;
	esac

	# No stack yet: offer the full bootstrap (init + up + open the browser).
	if [[ "$ASSUME_YES" != "true" ]] && ! can_prompt; then
		# Non-interactive (CI/CD, no terminal) - show manual instructions
		log_dim "  Verify installation:"
		echo ""
		echo "    obol version"
		echo ""
		print_start_instructions
		return 0
	fi

	echo ""
	log_info "Would you like to start the cluster now?"
	echo ""
	echo "  This will:"
	echo "    • Initialize cluster configuration"
	echo "    • Start the Obol Stack"
	echo "    • Open your browser to http://obol.stack"
	echo ""

	if ! confirm "Start cluster now?" "n"; then
		echo ""
		log_info "To start the cluster later, run:"
		echo ""
		echo "    obol stack init"
		echo "    obol stack up"
		echo ""
		log_dim "  Then open your browser to: http://obol.stack"
		echo ""
		return 0
	fi

	echo ""
	log_info "Starting bootstrap process..."

	# Run obol bootstrap
	if run_obol bootstrap; then
		return 0
	fi

	log_error "Bootstrap failed"
	echo ""
	log_info "You can start the cluster manually with:"
	echo ""
	echo "    obol stack init"
	echo "    obol stack up"
	echo "    obol agent init"
	echo ""
	return 1
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

	if [[ "$NONINTERACTIVE" == "true" ]]; then
		log_dim "  OBOL_NONINTERACTIVE=true: no prompts, optional steps are skipped"
	elif [[ "$ASSUME_YES" != "true" ]] && ! can_prompt; then
		log_dim "  No terminal available: no prompts, optional steps are skipped"
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
