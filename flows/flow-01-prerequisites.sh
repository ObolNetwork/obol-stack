#!/bin/bash
# Flow 01: Prerequisites — validate environment before any cluster work.
# No cluster needed. Checks: Docker, Ollama, obol binary.
source "$(dirname "$0")/lib.sh"

# Docker must be running
run_step "Docker daemon running" docker info

# Ollama must be serving
run_step_grep "Ollama serving models" "models" curl -sf http://localhost:11434/api/tags

# obol binary must exist and be executable
step "obol binary exists"
if [ -x "$OBOL" ]; then
    pass "obol binary exists at $OBOL"
else
    fail "obol binary not found at $OBOL"
fi

# obol version should return something
run_step_grep "obol version" "Version" "$OBOL" version

# Verify obol was built with Go 1.25+ (CLAUDE.md: "Go 1.25+")
step "obol built with Go 1.25+"
go_ver=$("$OBOL" version 2>&1 | grep "Go Version" | grep -oE "go[0-9]+\.[0-9]+\.[0-9]+" | head -1)
go_major=$(echo "${go_ver#go}" | cut -d. -f1)
go_minor=$(echo "${go_ver#go}" | cut -d. -f2)
if [ "${go_major:-0}" -gt 1 ] || { [ "${go_major:-0}" -eq 1 ] && [ "${go_minor:-0}" -ge 25 ]; }; then
    pass "obol Go version: $go_ver (>= 1.25)"
else
    fail "Go version too old: $go_ver (expected >= 1.25)"
fi

# obolup.sh installs: kubectl, helm, k3d, helmfile, k9s (getting-started §Install)
# Verify k3d is installed (required for cluster management)
step "k3d binary installed (cluster manager)"
if command -v "$OBOL_BIN_DIR/k3d" &>/dev/null || command -v k3d &>/dev/null; then
    k3d_ver=$("$OBOL_BIN_DIR/k3d" version 2>/dev/null | head -1 || k3d version 2>/dev/null | head -1)
    pass "k3d installed: ${k3d_ver:-available}"
else
    fail "k3d not found — install via: obolup.sh or brew install k3d"
fi

# Python packages required for paid inference (flow-08)
step "Python eth_account + httpx installed"
if ensure_payment_python_deps; then
    pass "eth_account + httpx available"
else
    fail "Missing Python packages and automatic venv setup failed — install eth-account httpx"
fi

# The default OpenClaw deployment depends on the published remote-signer chart.
step "remote-signer Helm chart version is published"
rs_version=$(remote_signer_chart_version)
if [ -z "$rs_version" ]; then
    fail "Could not parse remoteSignerChartVersion from internal/openclaw/openclaw.go"
elif remote_signer_chart_available "$rs_version"; then
    pass "obol/remote-signer $rs_version is available"
else
    helm repo update obol >/dev/null 2>&1 || true
    if remote_signer_chart_available "$rs_version"; then
        pass "obol/remote-signer $rs_version is available after helm repo update"
    else
        fail "obol/remote-signer $rs_version is not published in the configured Helm repo"
    fi
fi

emit_metrics
