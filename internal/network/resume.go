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
// deployments are left alone: a per-network `helmfile sync` renders and
// upgrades the node charts and restarts eRPC to re-register the local
// upstream, which is too costly to pay on every `obol stack up` when
// nothing was lost. `obol network sync --all` remains the explicit "force
// re-sync everything" path.
//
// Best-effort per deployment; failures warn and are joined into the result.
func ResumeInstalled(cfg *config.Config, u *ui.UI) error {
	var errs []error
	for _, ident := range ListDeployments(cfg) {
		namespace := deploymentNamespace(ident)
		health, err := resumeReleaseHealthFn(cfg, namespace)
		if err != nil {
			u.Warnf("Could not check network %s release status: %v", ident, err)
			errs = append(errs, err)
			continue
		}
		if health == helmcmd.ReleasesDeployed {
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
