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

emit_metrics
