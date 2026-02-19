---
name: test-backend
description: Launch and test the k3d or k3s backend lifecycle (init, up, kubectl, down, purge). Use when you want to run a full integration test of a stack backend.
user_invocable: true
metadata:
  author: obol-team
  version: "1.0.0"
  domain: testing
  triggers: test backend, test k3d, test k3s, integration test, flow test, backend test
  role: tester
  scope: validation
  output-format: report
---

# Test Backend Skill

Runs a full lifecycle integration test for the obol stack backend (k3d or k3s).

## Arguments

The skill accepts an optional argument specifying which backend to test:

- `k3s` - Test the k3s (bare-metal) backend only
- `k3d` - Test the k3d (Docker-based) backend only
- `all` - Test both backends sequentially (default)
- No argument defaults to `all`

Examples:
- `/test-backend k3s`
- `/test-backend k3d`
- `/test-backend all`
- `/test-backend` (same as `all`)

## Workflow

### 1. Pre-flight

- Build the obol binary: `go build -o .workspace/bin/obol ./cmd/obol` from the project root
- Verify the binary was created successfully
- Set `OBOL_DEVELOPMENT=true` and add `.workspace/bin` to PATH

### 2. Run Test Script

Based on the argument, run the appropriate test script(s) located alongside this skill:

- **k3s**: Run `.agents/skills/test-backend/scripts/test-k3s.sh`
- **k3d**: Run `.agents/skills/test-backend/scripts/test-k3d.sh`
- **all**: Run k3s first, then k3d (k3s requires sudo so test it first while credentials are fresh)

Execute the script via Bash tool from the project root directory. The scripts require:
- **k3s**: Linux, sudo access, k3s binary in `.workspace/bin/`
- **k3d**: Docker running, k3d binary in `.workspace/bin/`

### 3. Report Results

After each script completes, report:
- Total pass/fail counts (shown in the RESULTS line)
- Any specific test failures with their names
- Overall verdict: all green or needs attention

If a test script fails (non-zero exit), read the output to identify which test(s) failed and summarize.

## Important Notes

- The k3s backend requires **sudo access** - the user may need to enter their password
- The k3d backend requires **Docker to be running**
- Each test script performs its own cleanup (purge) before and after
- Tests are sequential and ordered: init -> up -> verify -> down -> restart -> purge
- Typical runtime: ~2-4 minutes per backend
- If the environment has issues (Docker not starting, k3s not installing), report the problem clearly rather than retrying endlessly
