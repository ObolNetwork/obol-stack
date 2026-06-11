package agentcrd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"gopkg.in/yaml.v3"
)

// Record-on-write for Agent CRs. The CR itself lives only in etcd; the host
// seeds (SOUL.md, skills) live in the data dir but nothing re-creates the CR
// after cluster recreation — so an agent's deployment, and any agent-backed
// ServiceOffer pointing at it, silently never came back. `obol agent new`
// persists the applied manifest here and `obol stack up` replays it
// (plans/stack-export-import.md, Phase 2).

func manifestStoreDir(cfg *config.Config) string {
	return filepath.Join(cfg.ConfigDir, "agents")
}

// ManifestPath returns the host-side record path for an agent's CR manifest.
func ManifestPath(cfg *config.Config, name string) string {
	return filepath.Join(manifestStoreDir(cfg), name+".yaml")
}

// PersistManifest writes the applied Agent CR manifest to the host store.
func PersistManifest(cfg *config.Config, name string, manifest map[string]any) error {
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("marshal agent manifest: %w", err)
	}
	if err := os.MkdirAll(manifestStoreDir(cfg), 0o700); err != nil {
		return err
	}
	path := ManifestPath(cfg, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RemoveManifest deletes the host-side record (on `obol agent delete`).
// Missing file is a no-op.
func RemoveManifest(cfg *config.Config, name string) error {
	err := os.Remove(ManifestPath(cfg, name))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ListPersistedManifests returns the agent names with a recorded manifest.
func ListPersistedManifests(cfg *config.Config) []string {
	entries, err := os.ReadDir(manifestStoreDir(cfg))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	return names
}

// ResumeAll re-applies every recorded Agent CR (namespace first — the
// controller's namespace handling is part of the reconcile loop that only
// runs once the CR exists). Called during `obol stack up`, BEFORE sell-offer
// replay so agent-backed ServiceOffers find their Agent. Best-effort per
// agent: one corrupt record must not block the rest.
func ResumeAll(cfg *config.Config, u *ui.UI) {
	names := ListPersistedManifests(cfg)
	if len(names) == 0 {
		return
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	u.Infof("Re-applying %d recorded agent(s)...", len(names))
	for _, name := range names {
		data, err := os.ReadFile(ManifestPath(cfg, name))
		if err != nil {
			u.Warnf("Could not read recorded agent %s: %v", name, err)
			continue
		}
		warnIfWalletWouldRegenerate(cfg, name, data, u)
		nsErr := kubectl.PipeCommands(bin, kubeconfig,
			[]string{"create", "namespace", Namespace(name), "--dry-run=client", "-o", "yaml"},
			[]string{"apply", "-f", "-"})
		if nsErr != nil {
			u.Warnf("Could not ensure namespace for agent %s: %v", name, nsErr)
		}
		if err := kubectl.Apply(bin, kubeconfig, data); err != nil {
			u.Warnf("Could not re-apply agent %s (run 'obol agent new %s' to recreate): %v", name, name, err)
			continue
		}
		u.Successf("Re-applied agent %s", name)
	}
}

// warnIfWalletWouldRegenerate flags the funds-stranding edge: replaying a
// wallet-bearing agent against a cluster that lost its keystore Secret
// (full recreation without a prior `obol stack import`) makes the
// controller mint a FRESH wallet — anything held at the old address is
// stranded unless the operator restores the keystore first. Best-effort:
// an unreachable cluster or a wallet-less agent stays silent.
func warnIfWalletWouldRegenerate(cfg *config.Config, name string, manifest []byte, u *ui.UI) {
	var doc struct {
		Spec struct {
			Wallet struct {
				Create bool `yaml:"create"`
			} `yaml:"wallet"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(manifest, &doc); err != nil || !doc.Spec.Wallet.Create {
		return
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	if err := kubectl.RunSilent(bin, kubeconfig,
		"get", "namespace", Namespace(name)); err != nil {
		// Namespace itself is gone — full recreation. The keystore Secret
		// check below would also fail, but distinguish nothing: same warning.
		u.Warnf("Agent %s declares a wallet but its namespace is gone — the controller will mint a NEW wallet on replay. "+
			"If the old wallet held funds, restore the keystore first: 'obol stack import <backup> --cluster-only' or 'obol agent wallet restore'.", name)
		return
	}
	if err := kubectl.RunSilent(bin, kubeconfig,
		"get", "secret", "remote-signer-keystore", "-n", Namespace(name)); err != nil {
		u.Warnf("Agent %s declares a wallet but namespace %s has no keystore Secret — the controller will mint a NEW wallet. "+
			"If the old wallet held funds, restore it first: 'obol stack import <backup> --cluster-only' or 'obol agent wallet restore'.",
			name, Namespace(name))
	}
}
