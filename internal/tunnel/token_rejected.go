package tunnel

import (
	"bufio"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ConnectorTokenRejected reports whether cloudflared's recent logs show
// Cloudflare refusing the connector token, e.g. the tunnel was deleted in the
// dashboard or its token rotated. `obol tunnel restart` can't fix that: a new
// pod with the same token hits the same error. Only a fresh token can.
func ConnectorTokenRejected(cfg *config.Config) bool {
	out, err := exec.Command(cfg.ToolPath("kubectl"),
		"--kubeconfig", filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"),
		"logs", "-n", tunnelNamespace, "-l", tunnelLabelSelector, "--tail=200").Output()
	if err != nil {
		return false
	}

	return tokenRejectedInLogs(string(out))
}

// tokenRejectedInLogs matches cloudflared's registration failure, e.g.
// `ERR Register tunnel error from server side error="Unauthorized: Tunnel not found"`.
func tokenRejectedInLogs(logs string) bool {
	sc := bufio.NewScanner(strings.NewReader(logs))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, "Register tunnel error") && strings.Contains(line, "Unauthorized") {
			return true
		}
	}

	return false
}

// TokenRejectedHint is the fix for a rejected connector token.
func TokenRejectedHint(hostname string) string {
	if hostname == "" {
		hostname = "<hostname>"
	}

	return fmt.Sprintf("Cloudflare rejected the connector token (tunnel deleted or token rotated in the dashboard).\n"+
		"  Copy the tunnel's token from the dashboard (or create a new tunnel) and run:\n"+
		"    obol tunnel setup <token> --hostname %s", hostname)
}
