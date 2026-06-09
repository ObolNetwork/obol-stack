package buy

import "github.com/ObolNetwork/obol-stack/internal/agentruntime"

const (
	hermesPythonPath = "/opt/hermes/.venv/bin/python3"
	hermesBuyPyPath  = "/data/.hermes/obol-skills/buy-x402/scripts/buy.py"

	openClawPythonPath = "python3"
	openClawBuyPyPath  = "/data/.openclaw/skills/buy-x402/scripts/buy.py"
)

// BuyPyCommand returns the in-pod argv prefix for the buy-x402 helper in the
// selected runtime. Hermes carries its own venv; OpenClaw exposes python3 on
// PATH and mounts skills under /data/.openclaw/skills.
func BuyPyCommand(runtime agentruntime.Runtime, args ...string) []string {
	python := hermesPythonPath
	script := hermesBuyPyPath
	if runtime == agentruntime.OpenClaw {
		python = openClawPythonPath
		script = openClawBuyPyPath
	}
	argv := []string{python, script}
	return append(argv, args...)
}
