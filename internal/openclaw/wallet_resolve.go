package openclaw

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ResolveWalletAddress returns the wallet address from the single OpenClaw
// instance's remote-signer. It follows the same convention as ResolveInstance:
//
//   - 0 instances: error
//   - 1 instance:  returns its wallet address
//   - 2+ instances: error listing available addresses
func ResolveWalletAddress(cfg *config.Config) (string, error) {
	ids, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", err
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no OpenClaw instances found — run 'obol agent init' first, or use --wallet")
	case 1:
		wallet, err := ReadWalletMetadata(DeploymentPath(cfg, ids[0]))
		if err != nil {
			return "", fmt.Errorf("wallet not found for instance %q: %w (use --wallet to specify manually)", ids[0], err)
		}
		return wallet.Address, nil
	default:
		var addrs []string
		for _, id := range ids {
			w, err := ReadWalletMetadata(DeploymentPath(cfg, id))
			if err != nil {
				continue
			}
			addrs = append(addrs, fmt.Sprintf("  %s (instance: %s)", w.Address, id))
		}
		return "", fmt.Errorf("multiple OpenClaw instances found, use --wallet to specify:\n%s", strings.Join(addrs, "\n"))
	}
}

// ResolveInstanceNamespace returns the Kubernetes namespace of the single
// OpenClaw instance. This is needed for port-forwarding to the remote-signer.
func ResolveInstanceNamespace(cfg *config.Config) (string, error) {
	ids, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", err
	}

	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no OpenClaw instances found — run 'obol agent init' first")
	case 1:
		return instanceNamespace(ids[0]), nil
	default:
		return "", fmt.Errorf("multiple OpenClaw instances found (%s), specify an instance", strings.Join(ids, ", "))
	}
}

// instanceNamespace returns the Kubernetes namespace for a given instance ID.
func instanceNamespace(id string) string {
	return fmt.Sprintf("openclaw-%s", id)
}
