package update

import (
	"os"
	"strings"
	"testing"
)

func TestSyncsBaseRelease(t *testing.T) {
	for _, tc := range []struct {
		selectors []string
		want      bool
	}{
		{nil, true},
		{[]string{"name=base"}, true},
		{[]string{"name=traefik", "name=base"}, true},
		{[]string{"name=traefik"}, false},
		{[]string{"name=base-extra"}, false},
	} {
		if got := syncsBaseRelease(tc.selectors); got != tc.want {
			t.Errorf("syncsBaseRelease(%v) = %v, want %v", tc.selectors, got, tc.want)
		}
	}
}

// The stale local-path StorageClass must be dropped BEFORE the helmfile sync
// that recreates it, and only when that sync includes the base release.
func TestApplyUpgrades_PreparesLocalPathStorageClassBeforeSync_SourceGuard(t *testing.T) {
	src, err := os.ReadFile("update.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func ApplyUpgrades(")
	if start < 0 {
		t.Fatal("ApplyUpgrades not found")
	}
	fn := body[start:]
	if end := strings.Index(fn[1:], "\nfunc "); end >= 0 {
		fn = fn[:end+1]
	}
	guard := strings.Index(fn, "if syncsBaseRelease(selectors) {")
	prep := strings.Index(fn, "kubectl.PrepareLocalPathStorageClass(cfg, u)")
	crds := strings.Index(fn, "applyChartCRDs(")
	sync := strings.Index(fn, `helmfileArgs = append(helmfileArgs, "sync")`)
	if guard < 0 || prep < 0 || crds < 0 || sync < 0 {
		t.Fatalf("missing step: guard=%d prep=%d crds=%d sync=%d", guard, prep, crds, sync)
	}
	if !(guard < prep && prep < crds && crds < sync) {
		t.Fatalf("wrong order: guard=%d prep=%d crds=%d sync=%d", guard, prep, crds, sync)
	}
}
