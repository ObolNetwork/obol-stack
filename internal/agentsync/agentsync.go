// Package agentsync re-deploys helmfile-based agent instances
// (applications/hermes/<id>, applications/openclaw/<id>) from their on-disk
// deployment directories. Shared by `obol stack import` (unconditional sync so
// restored secrets/tokens line up) and the stack-up/sell-resume record replay
// (internal/replay: only instances whose helm release is missing or failed).
package agentsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// Options controls SyncInstances.
type Options struct {
	// OnlyMissing skips instances whose namespace already holds deployed
	// helm releases. `helmfile sync` per agent costs a repo refresh plus a
	// chart render and upgrade (tens of seconds), so the stack-up replay only
	// pays it when the cluster actually lost the release.
	OnlyMissing bool
}

// Swappable for tests.
var (
	syncFns = map[agentruntime.Runtime]func(*config.Config, string, *ui.UI) error{
		agentruntime.Hermes:   hermes.Sync,
		agentruntime.OpenClaw: openclaw.Sync,
	}
	releaseHealthFn = func(cfg *config.Config, namespace string) (helmcmd.ReleaseHealth, error) {
		return helmcmd.NamespaceReleaseHealth(cfg.ToolPath("helm"), filepath.Join(cfg.ConfigDir, "kubeconfig.yaml"), namespace)
	}
)

// ListInstances returns the instance IDs with a deployment dir for runtime.
func ListInstances(cfg *config.Config, runtime agentruntime.Runtime) []string {
	entries, err := os.ReadDir(filepath.Join(cfg.ConfigDir, "applications", string(runtime)))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// SyncInstances re-deploys every hermes/openclaw instance from its on-disk
// helmfile. Best-effort per instance: failures warn and are joined into the
// returned error so callers can count the step as warned.
func SyncInstances(cfg *config.Config, u *ui.UI, opts Options) error {
	var errs []error
	for _, runtime := range []agentruntime.Runtime{agentruntime.Hermes, agentruntime.OpenClaw} {
		for _, id := range ListInstances(cfg, runtime) {
			if opts.OnlyMissing {
				health, err := releaseHealthFn(cfg, agentruntime.Namespace(runtime, id))
				if err != nil {
					u.Warnf("Could not check %s/%s release status: %v", runtime, id, err)
					errs = append(errs, err)
					continue
				}
				if health == helmcmd.ReleasesDeployed {
					continue
				}
				u.Infof("Re-deploying %s instance %s (release %s)...", runtime, id, health)
			} else {
				u.Infof("Syncing %s instance %s...", runtime, id)
			}
			if err := syncFns[runtime](cfg, id, u); err != nil {
				hint := "obol agent sync " + id
				if runtime == agentruntime.OpenClaw {
					hint = "obol openclaw sync " + id
				}
				u.Warnf("%s sync %s failed (run '%s' manually): %v", runtime, id, hint, err)
				errs = append(errs, fmt.Errorf("%s/%s: %w", runtime, id, err))
			}
		}
	}
	return errors.Join(errs...)
}
