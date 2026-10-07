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
	origSync, origHealth := resumeSyncFn, resumeReleaseHealthFn
	t.Cleanup(func() { resumeSyncFn, resumeReleaseHealthFn = origSync, origHealth })

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

	err := ResumeInstalled(cfg, ui.New(false))
	if err == nil {
		t.Fatal("expected the failed sync to surface as an error")
	}
	if want := []string{"aztec/node1", "ethereum/mainnet"}; !reflect.DeepEqual(synced, want) {
		t.Fatalf("synced = %v, want %v", synced, want)
	}
}

func TestResumeInstalled_NoNetworksIsNoop(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	if err := ResumeInstalled(cfg, ui.New(false)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
