package app

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ResumeAll re-syncs every installed app deployment into the (possibly
// freshly recreated) cluster. App deployments are declarative on disk
// (helmfile.yaml + values.yaml under applications/<app>/<id>/) but their
// cluster resources live in etcd, which `obol stack down` + recreate
// destroys — without this replay a fresh `stack up` comes back with the
// deployment directories still on disk but no matching cluster resources,
// and any `obol sell http` offer gating an app upstream dangles.
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

	u.Infof("Re-syncing %d installed app(s)...", len(ids))

	for _, id := range ids {
		if err := Sync(cfg, u, id); err != nil {
			u.Warnf("Could not re-sync app %s (run 'obol app sync %s' to retry): %v", id, id, err)
		}
	}
}
