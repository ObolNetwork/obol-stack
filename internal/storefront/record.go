package storefront

import (
	"encoding/json"
	"os"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ReconcileRecorded re-applies the operator's recorded storefront branding into
// the cluster during `obol stack up`.
//
// `obol sell info set` writes the profile to both the x402/obol-storefront-profile
// ConfigMap (etcd, destroyed by `obol stack down`) and a host-side record at
// $CONFIG_DIR/storefront/profile.json (survives cluster recreation). Without this
// replay a fresh `stack up` would come back with the branding on disk but no
// matching ConfigMap, so the controller would publish the default catalog envelope.
//
// The host file is the source of truth. No record on disk => nothing to do.
// Best-effort: a failure warns but never blocks stack-up.
func ReconcileRecorded(cfg *config.Config, u *ui.UI) {
	data, err := os.ReadFile(ProfileLocalPath(cfg))
	if err != nil {
		return // no recorded storefront profile — nothing to replay
	}
	profile, err := ParseProfile(string(data))
	if err != nil {
		u.Warnf("Could not read recorded storefront profile: %v", err)
		return
	}
	if profile == nil {
		return
	}
	if err := kubectl.EnsureCluster(cfg); err != nil {
		return
	}

	manifest, err := ConfigMapManifest(*profile)
	if err != nil {
		u.Warnf("Could not render recorded storefront profile: %v", err)
		return
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		u.Warnf("Could not render recorded storefront profile: %v", err)
		return
	}

	bin, kubeconfig := kubectl.Paths(cfg)
	if err := kubectl.Apply(bin, kubeconfig, payload); err != nil {
		u.Warnf("Could not reconcile recorded storefront profile: %v", err)
		return
	}
	u.Detail("Restored", "storefront branding")
}
