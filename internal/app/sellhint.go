package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// maxSellHints caps how many per-service sell suggestions are printed
// after a sync; charts with many Services (umbrella charts) would
// otherwise drown the success output.
const maxSellHints = 3

// PrintSellHint discovers the Services a just-synced app exposes and
// prints a ready-to-run `obol sell http` command for each, connecting app
// installs to the monetization flow. Best-effort: any discovery failure
// (no kubeconfig, kubectl missing, no Services yet) prints nothing.
func PrintSellHint(cfg *config.Config, u *ui.UI, deploymentIdentifier string) {
	appName, id, err := parseDeploymentIdentifier(deploymentIdentifier)
	if err != nil {
		return
	}

	namespace := fmt.Sprintf("%s-%s", appName, id)

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); err != nil {
		return
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")
	if _, err := os.Stat(kubectlBinary); err != nil {
		return
	}

	cmd := exec.Command(kubectlBinary, "get", "services", "-n", namespace,
		"-o", `jsonpath={range .items[*]}{.metadata.name} {.spec.ports[0].port}{"\n"}{end}`)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)

	out, err := cmd.Output()
	if err != nil {
		return
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")

	printed := 0

	for _, line := range lines {
		name, port, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || name == "" || port == "" {
			continue
		}

		if printed == 0 {
			u.Blank()
			u.Print("To put this app on sale behind x402 payments:")
		}

		if printed == maxSellHints {
			u.Printf("  (more services: obol kubectl get svc -n %s)", namespace)
			break
		}

		u.Printf("  obol sell http %s --upstream %s --port %s --namespace %s --per-request 0.001 --pay-to 0x<wallet>",
			name, name, port, namespace)

		printed++
	}
}
