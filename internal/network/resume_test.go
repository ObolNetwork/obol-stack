package network

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/helmcmd"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

func TestResumeInstalled_SyncsOnlyMissingOrFailed(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	for _, p := range []string{"ethereum/hoodi", "ethereum/mainnet", "aztec/node1"} {
		if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "networks", p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	origSync, origHealth, origRegister := resumeSyncFn, resumeReleaseHealthFn, resumeRegisterERPCFn
	t.Cleanup(func() { resumeSyncFn, resumeReleaseHealthFn, resumeRegisterERPCFn = origSync, origHealth, origRegister })

	health := map[string]helmcmd.ReleaseHealth{
		"ethereum-hoodi":   helmcmd.ReleasesDeployed,
		"ethereum-mainnet": helmcmd.ReleasesUnhealthy,
		"aztec-node1":      helmcmd.ReleasesMissing,
	}
	resumeReleaseHealthFn = func(_ *config.Config, ns string) (helmcmd.ReleaseHealth, error) {
		return health[ns], nil
	}
	var synced []string
	resumeSyncFn = func(_ *config.Config, _ *ui.UI, ident string) error {
		synced = append(synced, ident)
		if ident == "aztec/node1" {
			return errors.New("boom")
		}
		return nil
	}

	var registered []string
	resumeRegisterERPCFn = func(_ *config.Config, networkType, id string) error {
		registered = append(registered, networkType+"/"+id)
		return nil
	}

	err := ResumeInstalled(cfg, ui.New(false))
	if err == nil {
		t.Fatal("expected the failed sync to surface as an error")
	}
	if want := []string{"aztec/node1", "ethereum/mainnet"}; !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced = %v, want %v", synced, want)
	}
	// Healthy nodes skip the sync but must be put back into the eRPC
	// ConfigMap, which stack up re-renders without local upstreams.
	if want := []string{"ethereum/hoodi"}; !reflect.DeepEqual(registered, want) {
		t.Fatalf("registered with eRPC = %v, want %v", registered, want)
	}
}

func TestResumeInstalled_SkipsERPCForHealthyNonEthereum(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := os.MkdirAll(filepath.Join(cfg.ConfigDir, "networks", "aztec", "node1"), 0o755); err != nil {
		t.Fatal(err)
	}
	origHealth, origRegister := resumeReleaseHealthFn, resumeRegisterERPCFn
	t.Cleanup(func() { resumeReleaseHealthFn, resumeRegisterERPCFn = origHealth, origRegister })

	resumeReleaseHealthFn = func(*config.Config, string) (helmcmd.ReleaseHealth, error) {
		return helmcmd.ReleasesDeployed, nil
	}
	resumeRegisterERPCFn = func(*config.Config, string, string) error {
		t.Fatal("aztec has no execution RPC to register with eRPC")
		return nil
	}

	if err := ResumeInstalled(cfg, ui.New(false)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResumeInstalled_NoNetworksIsNoop(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := ResumeInstalled(cfg, ui.New(false)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
