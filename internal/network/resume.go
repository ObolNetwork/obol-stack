package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Swappable for tests.
var (
	resumeSyncFn          = Sync
	resumeRegisterERPCFn  = RegisterERPCUpstream
	resumeReleaseHealthFn = func(cfg *config.Config, namespace string) (helmcmd.ReleaseHealth, error) {
		return helmcmd.NamespaceReleaseHealth(cfg.ToolPath("helm"), filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), namespace)
	}
)

// ListDeployments returns every installed local network deployment as
// "<network>/<id>", sorted.
func ListDeployments(cfg *config.Config) []string {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")
	networkDirs, err := os.ReadDir(networksDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, nd := range networkDirs {
		if !nd.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(networksDir, nd.Name()))
		if err != nil {
			continue
		}
		for _, d := range deployments {
			if d.IsDir() {
				out = append(out, nd.Name()+"/"+d.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

// ResumeInstalled re-deploys installed local networks whose helm releases are
// missing (fresh / recreated cluster) or not in "deployed" state. Healthy
// deployments skip the costly per-network `helmfile sync`, but ethereum ones
// are re-registered with eRPC: `obol stack up` re-renders the eRPC ConfigMap
// without local upstreams (they aren't recorded), so without this a healthy
// node silently drops out of /rpc after the next eRPC restart. Registration
// merges by id and only rolls eRPC when the config changed. `obol network
// sync --all` remains the explicit "force re-sync everything" path.
// Deployments recorded as stopped (`obol network stop`) are skipped.
//
// Best-effort per deployment; failures warn and are joined into the result.
func ResumeInstalled(cfg *config.Config, u *ui.UI) error {
	var errs []error
	for _, ident := range ListDeployments(cfg) {
		namespace := deploymentNamespace(ident)

		// A stopped node (obol network stop) stays down: no sync, no
		// eRPC registration. obol network start brings it back.
		if networkType, id, _ := strings.Cut(ident, "/"); deploymentStopped(cfg, networkType, id) {
			u.Infof("Network %s is stopped; leaving it down (obol network start %s)", ident, ident)
			continue
		}

		health, err := resumeReleaseHealthFn(cfg, namespace)
		if err != nil {
			u.Warnf("Could not check network %s release status: %v", ident, err)
			errs = append(errs, err)
			continue
		}
		if health == helmcmd.ReleasesDeployed {
			networkType, id, _ := strings.Cut(ident, "/")
			if networkType != "ethereum" {
				continue
			}

			if err := resumeRegisterERPCFn(cfg, networkType, id); err != nil {
				u.Warnf("Could not re-register %s with eRPC (run 'obol network sync %s'): %v", ident, ident, err)
				errs = append(errs, fmt.Errorf("%s: %w", ident, err))
			}

			continue
		}
		u.Infof("Re-deploying network %s (release %s)...", ident, health)
		if err := resumeSyncFn(cfg, u, ident); err != nil {
			u.Warnf("Could not re-deploy network %s (run 'obol network sync %s'): %v", ident, ident, err)
			errs = append(errs, fmt.Errorf("%s: %w", ident, err))
		}
	}
	return errors.Join(errs...)
}

// deploymentNamespace maps "<network>/<id>" to its namespace "<network>-<id>"
// (see Sync).
func deploymentNamespace(ident string) string {
	return strings.Replace(ident, "/", "-", 1)
}
