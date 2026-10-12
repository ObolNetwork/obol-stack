package buy

import "github.com/ObolNetwork/obol-stack/internal/agentruntime"

const (
	hermesPythonPath = "/opt/hermes/.venv/bin/python3"
	hermesBuyPyPath  = "/data/.hermes/obol-skills/buy-x402/scripts/buy.py"
)

// BuyPyCommand returns the in-pod argv prefix for the buy-x402 helper in the
// selected runtime. Hermes (the only runtime) carries its own venv.
func BuyPyCommand(_ agentruntime.Runtime, args ...string) []string {
	argv := []string{hermesPythonPath, hermesBuyPyPath}
	return append(argv, args...)
}
