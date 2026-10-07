package stack

import (
	"os"
	"strings"
	"testing"
)

// TestPurge_OffersExportWithoutForce is a source-level guard: Purge must offer
// the full stack export for a plain purge too, not just --force. A plain purge
// keeps the data dir but deletes the config dir, which holds every agent's
// keystore password (values-remote-signer.yaml), so the kept keystores become
// unusable; the cluster (and sub-agent wallets stored only as in-cluster
// Secrets) is destroyed in both modes. The prompt used to sit behind
// `if force {`, so `obol stack purge` stranded wallets with no warning.
func TestPurge_OffersExportWithoutForce(t *testing.T) {
	src, err := os.ReadFile("stack.go")
	if err != nil {
		t.Fatalf("read stack.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func Purge(")
	if start < 0 {
		t.Fatal("func Purge not found in stack.go")
	}
	purge := body[start:]
	if end := strings.Index(purge[1:], "\nfunc "); end >= 0 {
		purge = purge[:end+1]
	}

	call := strings.Index(purge, "stackbackup.PromptExportBeforePurge(cfg, u, force)")
	if call < 0 {
		t.Fatal("Purge must call stackbackup.PromptExportBeforePurge(cfg, u, force)")
	}
	if strings.Contains(purge[:call], "if force {") {
		t.Error("PromptExportBeforePurge must not be gated on --force: a plain purge deletes keystore passwords too")
	}
	if destroy := strings.Index(purge, "backend.Destroy("); destroy >= 0 && destroy < call {
		t.Error("the export prompt must run BEFORE the cluster is destroyed — sub-agent wallets live only in-cluster")
	}
	if rm := strings.Index(purge, "os.RemoveAll(cfg.ConfigDir)"); rm >= 0 && rm < call {
		t.Error("the export prompt must run BEFORE the config dir (keystore passwords) is removed")
	}
}
