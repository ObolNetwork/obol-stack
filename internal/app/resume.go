package app

import (
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ResumeAll re-deploys installed app deployments whose helm release is
// missing or failed, e.g. after the cluster was recreated: app deployments
// are declarative on disk (helmfile.yaml + values.yaml under
// applications/<app>/<id>/) but their cluster resources live in etcd, so a
// fresh `stack up` would otherwise come back without them, and any
// `obol sell http` offer gating an app upstream would dangle.
//
// A release that is already deployed is left alone, like networks and Hermes
// instances in the same replay: a `stack up` must not apply a pending
// values.yaml edit to (and possibly restart) a live workload such as a
// distributed validator. `obol app sync` applies edits explicitly.
//
// Best-effort, mirroring agentcrd.ResumeAll: a failed app warns and does
// not block stack-up.
func ResumeAll(cfg *config.Config, u *ui.UI) {
	ids, err := ListInstanceIDs(cfg)
	if err != nil {
		u.Warnf("Could not list installed apps: %v", err)
		return
	}

	if len(ids) == 0 {
		return
	}

	var toSync, deployed []string

	for _, id := range ids {
		switch state, err := resumeState(cfg, id); {
		case err != nil:
			u.Warnf("Could not check app %s, leaving it as it is (run 'obol app sync %s' to deploy it): %v", id, id, err)
		case state != nil && state.Status == "deployed":
			deployed = append(deployed, id)
		default:
			toSync = append(toSync, id)
		}
	}

	if len(deployed) > 0 {
		u.Dim(fmt.Sprintf("  %d app(s) already deployed, left as they are: %s. Run 'obol app sync <app>/<id>' to apply values.yaml changes.",
			len(deployed), strings.Join(deployed, ", ")))
	}

	if len(toSync) == 0 {
		return
	}

	u.Infof("Re-deploying %d app(s) with a missing or failed release: %s", len(toSync), strings.Join(toSync, ", "))

	for _, id := range toSync {
		if err := Sync(cfg, u, id); err != nil {
			u.Warnf("Could not re-sync app %s (run 'obol app sync %s' to retry): %v", id, id, err)
		}
	}
}

// resumeState returns the live helm release of an app deployment, nil when
// it does not exist.
func resumeState(cfg *config.Config, identifier string) (*releaseState, error) {
	appName, id, err := parseDeploymentIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	meta, err := loadAppMeta(deploymentDirFor(cfg, appName, id), appName, id)
	if err != nil {
		return nil, err
	}

	return liveRelease(cfg, meta.Namespace, meta.Release)
}
