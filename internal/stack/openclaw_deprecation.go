package stack

import (
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// listNamespacesFn lists cluster namespace names. Swappable in tests.
var listNamespacesFn = func(cfg *config.Config) ([]string, error) {
	bin, kubeconfig := kubectl.Paths(cfg)

	out, err := kubectl.Output(bin, kubeconfig, "get", "namespaces",
		"--request-timeout=10s", "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}

	return strings.Fields(out), nil
}

// warnLegacyOpenClawNamespaces prints the OpenClaw deprecation and wallet
// migration steps once when the cluster still runs openclaw-* instances.
// Best effort: lookup failures are ignored and never fail `stack up`.
func warnLegacyOpenClawNamespaces(cfg *config.Config, u *ui.UI) {
	names, err := listNamespacesFn(cfg)
	if err != nil {
		return
	}

	var legacy []string

	for _, ns := range names {
		if strings.HasPrefix(ns, "openclaw-") {
			legacy = append(legacy, ns)
		}
	}

	if len(legacy) == 0 {
		return
	}

	u.Blank()
	u.Warnf("Found OpenClaw instance namespace(s): %s", strings.Join(legacy, ", "))

	for _, line := range openclaw.DeprecationNotice {
		u.Warn("  " + line)
	}
}
