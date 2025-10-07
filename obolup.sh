#!/usr/bin/env bash

set -Eeuo pipefail

readonly OBOL_CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/obol"
readonly OBOL_DATA_DIR="${OBOL_CONFIG_DIR}/data"
readonly OBOL_BIN_DIR="${OBOL_CONFIG_DIR}/bin"

readonly cmd_k3d="${OBOL_BIN_DIR}/k3d"
readonly cmd_helmfile="${OBOL_BIN_DIR}/helmfile"

readonly CLUSTER_NAME="obol-stack"
readonly KUBECONFIG_FILE="${OBOL_CONFIG_DIR}/kubeconfig.yaml"

readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1" >&2; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $1" >&2; exit 1; }

command_exists() {
    command -v "$1" >/dev/null 2>&1
}

tolower() {
    echo "$1" | awk '{print tolower($0)}'
}

detect_platform() {
    local uname_s=$(uname -s)
    local platform=$(tolower "${OBOLUP_PLATFORM:-$uname_s}")
    
    case $platform in
        linux) echo "linux" ;;
        darwin|mac*) echo "darwin" ;;
        *)
            log_error "unsupported platform: $platform (only Linux and macOS are supported)"
            ;;
    esac
}

detect_architecture() {
    local uname_m=$(uname -m)
    local arch=$(tolower "${OBOLUP_ARCH:-$uname_m}")
    
    if [ "${arch}" = "x86_64" ]; then
        if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ]; then
            echo "arm64"
        else
            echo "amd64"
        fi
    elif [ "${arch}" = "arm64" ] || [ "${arch}" = "aarch64" ]; then
        echo "arm64"
    else
        echo "amd64"
    fi
}

declare -A TOOLS=(
    ["k3d_version"]="v5.7.5"
    ["k3d_url_linux_amd64"]="https://github.com/k3d-io/k3d/releases/download/v5.7.5/k3d-linux-amd64"
    ["k3d_url_linux_arm64"]="https://github.com/k3d-io/k3d/releases/download/v5.7.5/k3d-linux-arm64"
    ["k3d_url_darwin_amd64"]="https://github.com/k3d-io/k3d/releases/download/v5.7.5/k3d-darwin-amd64"
    ["k3d_url_darwin_arm64"]="https://github.com/k3d-io/k3d/releases/download/v5.7.5/k3d-darwin-arm64"
    ["k3d_platforms"]="linux,darwin"
    ["k3d_compression"]="none"
    
    ["helmfile_version"]="v1.1.7"
    ["helmfile_url_linux_amd64"]="https://github.com/helmfile/helmfile/releases/download/v1.1.7/helmfile_1.1.7_linux_amd64.tar.gz"
    ["helmfile_url_linux_arm64"]="https://github.com/helmfile/helmfile/releases/download/v1.1.7/helmfile_1.1.7_linux_arm64.tar.gz"
    ["helmfile_url_darwin_amd64"]="https://github.com/helmfile/helmfile/releases/download/v1.1.7/helmfile_1.1.7_darwin_amd64.tar.gz"
    ["helmfile_url_darwin_arm64"]="https://github.com/helmfile/helmfile/releases/download/v1.1.7/helmfile_1.1.7_darwin_arm64.tar.gz"
    ["helmfile_platforms"]="linux,darwin"
    ["helmfile_compression"]="tar.gz"
)

validate_docker_environment() {
    if ! command_exists docker; then
        log_error "Docker is required for k3d. Please install Docker first."
    fi
    
    if ! docker info >/dev/null 2>&1; then
        log_error "Docker daemon is not accessible. Please ensure Docker is running and your user has permission to access it."
    fi
    
    if ! docker ps >/dev/null 2>&1; then
        log_error "Cannot list Docker containers. Check Docker socket permissions or add your user to the docker group."
    fi
    
    if docker info 2>&1 | grep -iq "No cpuset support"; then
        log_error "Docker does not have cpuset support. k3d requires cpuset cgroup controller. Please ensure your kernel has CONFIG_CPUSETS=y and cgroup v2 is properly configured."
    fi
    
    log_info "✓ Docker is installed and accessible"
}

setup_directories() {
    log_info "Setting up Obol directories..."
    
    if [ ! -d "$OBOL_CONFIG_DIR" ]; then
        mkdir -p "$OBOL_CONFIG_DIR"
        log_info "Created config directory: $OBOL_CONFIG_DIR"
    fi
    
    if [ ! -d "$OBOL_BIN_DIR" ]; then
        mkdir -p "$OBOL_BIN_DIR"
        log_info "Created bin directory: $OBOL_BIN_DIR"
    fi
    
}

install_tool() {
    local tool_name="$1"
    local platform="$2"
    local arch="$3"
    local target="${OBOL_BIN_DIR}/${tool_name}"
    
    local supported_platforms="${TOOLS[${tool_name}_platforms]}"
    if [[ ! ",${supported_platforms}," =~ ",${platform}," ]]; then
        log_error "${tool_name} is not supported on ${platform}"
    fi
    
    if [ -f "$target" ]; then
        log_info "✓ ${tool_name} is already installed"
        return 0
    fi
    
    local url_key="${tool_name}_url_${platform}_${arch}"
    local tool_url="${TOOLS[$url_key]}"
    
    if [ -z "$tool_url" ]; then
        log_error "No URL found for ${tool_name} on ${platform}/${arch}"
    fi
    
    log_info "Installing ${tool_name} to ${OBOL_BIN_DIR}..."
    
    local temp_file=$(mktemp)
    
    if ! curl -sSLf -o "$temp_file" "$tool_url"; then
        rm -f "$temp_file"
        log_error "Failed to download ${tool_name} from $tool_url"
    fi
    
    local compression="${TOOLS[${tool_name}_compression]}"
    case "$compression" in
        tar.gz)
            local temp_dir=$(mktemp -d)
            tar -xzf "$temp_file" -C "$temp_dir"
            mv "$temp_dir/${tool_name}" "$target"
            rm -rf "$temp_dir" "$temp_file"
            ;;
        none)
            mv "$temp_file" "$target"
            ;;
        *)
            rm -f "$temp_file"
            log_error "Unsupported compression type: $compression"
            ;;
    esac
    
    chmod +x "$target"
    
    log_info "✓ ${tool_name} installed successfully to ${target}"
}

setup_k3d_cluster() {
    
    
    log_info "Checking for existing k3d cluster '${CLUSTER_NAME}'..."
    
    if ! "${cmd_k3d}" cluster list >/dev/null 2>&1; then
        log_error "k3d cannot connect to Docker"
    fi
    
    if "${cmd_k3d}" cluster list 2>/dev/null | grep -q "^${CLUSTER_NAME} "; then
        log_info "✓ k3d cluster '${CLUSTER_NAME}' already exists"
        return 0
    fi
    
    log_info "Creating k3d cluster '${CLUSTER_NAME}'..."
    # k3d  cluster create demo --api-port 6550  --servers 3 --port 8080:80@loadbalancer --volume $(pwd)/sample:/src@all --wait    
    if ! "${cmd_k3d}" cluster create "${CLUSTER_NAME}" \
        --servers 3 \
        --agents 0 \
        --api-port 6443 \
        --port 8080:8080@loadbalancer \
        --wait; then
        log_error "Failed to create k3d cluster. Check Docker permissions and logs above."
    fi
    
    log_info "Writing kubeconfig to ${KUBECONFIG_FILE}..."
    "${cmd_k3d}" kubeconfig write "${CLUSTER_NAME}" --output "${KUBECONFIG_FILE}" --overwrite
    
    log_info "✓ k3d cluster '${CLUSTER_NAME}' created successfully"
    log_info "  Kubeconfig: ${KUBECONFIG_FILE}"
    log_info "  Access cluster: export KUBECONFIG=${KUBECONFIG_FILE} && kubectl cluster-info"
}

banner() {
    cat <<'EOF'

.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo
  ___  ____   ___  _    _   _ ____
 / _ \| __ ) / _ \| |  | | | |  _ \
| | | |  _ \| | | | |  | | | | |_) |
| |_| | |_) | |_| | |__| |_| |  __/
 \___/|____/ \___/|_____\___/|_|

   Local Kubernetes Cluster Bootstrap

.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo.oOo

EOF
}

clean_all() {
    local force="$1"
    
    if [ ! -d "$OBOL_DATA_DIR" ] && [ ! -d "$OBOL_BIN_DIR" ] && ! "${cmd_k3d}" cluster list 2>/dev/null | grep -q "^${CLUSTER_NAME} "; then
        log_info "Nothing to clean"
        return 0
    fi
    
    if [ -f "${cmd_k3d}" ] && "${cmd_k3d}" cluster list 2>/dev/null | grep -q "^${CLUSTER_NAME} "; then
        log_warn "Deleting k3d cluster '${CLUSTER_NAME}' and its Docker containers"
        if "${cmd_k3d}" cluster delete "${CLUSTER_NAME}"; then
            log_info "✓ Deleted k3d cluster '${CLUSTER_NAME}'"
        else
            log_warn "Failed to delete k3d cluster '${CLUSTER_NAME}'"
        fi
    fi
    
    if [ -d "$OBOL_DATA_DIR" ]; then
        log_warn "This will delete $OBOL_DATA_DIR and all its contents"
        read -p "Are you sure? (yes/no): " confirm
        
        if [ "$confirm" = "yes" ]; then
            rm -rf "$OBOL_DATA_DIR"
            log_info "✓ Cleaned $OBOL_DATA_DIR"
        else
            log_info "Clean cancelled"
            return 0
        fi
    fi
    
    if [ "$force" = "true" ]; then
        if [ -d "$OBOL_BIN_DIR" ]; then
            log_warn "This will delete $OBOL_BIN_DIR and all binaries"
            read -p "Are you sure? (yes/no): " confirm
            
            if [ "$confirm" = "yes" ]; then
                rm -rf "$OBOL_BIN_DIR"
                log_info "✓ Cleaned $OBOL_BIN_DIR"
            else
                log_info "Binary clean cancelled"
            fi
        fi
    else
        log_info "Binaries preserved. Use --clean --force to remove binaries."
    fi
}

usage() {
    cat <<EOF
Usage: curl -sSfL https://stack.obol.org/obolup.sh | bash
       obolup.sh --clean [--force]

Bootstrap a local Kubernetes environment for Obol Stack.

Installs dependencies to ${OBOL_BIN_DIR}:
    - k3d (Linux and macOS) - k3s in Docker
    - helmfile (Linux and macOS) - Declarative Helm charts deployment

Options:
    --debug         Enable debug mode (bash -x)
    --clean         Remove all obolup data files
    --clean --force Remove all obolup data files and binaries

Supported Platforms:
    - Linux (amd64, arm64)
    - macOS (amd64, arm64)

Binaries are installed to: ${OBOL_BIN_DIR}
Data is stored in: ${OBOL_DATA_DIR}

EOF
    exit "${1:-0}"
}

main() {
    local debug_mode="false"
    
    while [ $# -gt 0 ]; do
        case "$1" in
            --debug)
                debug_mode="true"
                shift
                ;;
            --clean)
                banner
                local force="false"
                if [ $# -gt 1 ] && [ "$2" = "--force" ]; then
                    force="true"
                fi
                clean_all "$force"
                exit 0
                ;;
            *)
                shift
                ;;
        esac
    done
    
    if [ "$debug_mode" = "true" ]; then
        set -x
    fi
    
    banner
    
    local platform=$(detect_platform)
    local arch=$(detect_architecture)
    
    log_info "Platform: ${platform}"
    log_info "Architecture: ${arch}"
    echo ""
    
    setup_directories
    echo ""
    
    install_tool "k3d" "$platform" "$arch"
    echo ""
    
    install_tool "helmfile" "$platform" "$arch"
    echo ""
    
    validate_docker_environment
    echo ""

    setup_k3d_cluster
}

main "$@"
