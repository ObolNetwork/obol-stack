package network

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// deploymentStateFile is the only per-deployment state under
// networks/<network>/<id>/. Everything else there (helmfile, Chart.yaml,
// templates/) is a copy of the network files embedded in obol.
const deploymentStateFile = "values.yaml"

// refreshDeploymentFiles replaces a deployment's copied network files with
// the ones embedded in this obol binary, keeping values.yaml. Without it,
// image pin bumps and template fixes never reach existing installs: sync
// would keep rendering whatever was copied at install time.
//
// The fresh copy is staged in a sibling directory first, so a failure leaves
// the deployment untouched. Returns false (nothing changed) when this obol
// no longer embeds the network.
func refreshDeploymentFiles(networkName, deploymentDir string) (bool, error) {
	if _, err := embed.ReadEmbeddedNetworkFile(networkName, "helmfile.yaml.gotmpl"); err != nil {
		return false, nil
	}

	staging, err := os.MkdirTemp(filepath.Dir(deploymentDir), "."+filepath.Base(deploymentDir)+"-refresh-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(staging)

	if err := embed.CopyNetwork(networkName, staging); err != nil {
		return false, fmt.Errorf("stage network files: %w", err)
	}
	// The values template only seeds values.yaml at install.
	_ = os.Remove(filepath.Join(staging, "values.yaml.gotmpl"))

	entries, err := os.ReadDir(deploymentDir)
	if err != nil {
		return false, err
	}

	for _, e := range entries {
		if e.Name() == deploymentStateFile {
			continue
		}

		if err := os.RemoveAll(filepath.Join(deploymentDir, e.Name())); err != nil {
			return false, fmt.Errorf("remove stale %s: %w", e.Name(), err)
		}
	}

	staged, err := os.ReadDir(staging)
	if err != nil {
		return false, err
	}

	for _, e := range staged {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(deploymentDir, e.Name())); err != nil {
			return false, fmt.Errorf("install refreshed %s: %w", e.Name(), err)
		}
	}

	return true, nil
}
