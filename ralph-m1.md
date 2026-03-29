# Ralph M1: Validate Monetize Inference Selling

Read this file for full instructions. Load skills: obol-stack-dev, agentic-economy.

## Gate
Run these flow scripts (REAL user path, not unit tests):
1. `bash flows/flow-06-sell-setup.sh`
2. `bash flows/flow-07-sell-verify.sh`
3. `bash flows/flow-10-anvil-facilitator.sh`
4. `bash flows/flow-08-buy.sh`

After flow-06: also run `obol sell probe flow-qwen -n llm`

## Rules
- Use built binary (`obol`), not `go run`
- Wait for real heartbeat (5 min), don't exec monetize.py directly
- Route through Traefik (`obol.stack:8080`), not pod IPs
- Fix failures in Go code OR flow scripts
- Commit with prefix `fix(m1):`
- Write findings to `/tmp/m1-findings.md`
