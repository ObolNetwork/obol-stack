package network

import (
	"os"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestRecordedRPCsRoundTrip(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	// Absent record reads as nil, nil.
	rec, err := readRecordedRPCs(cfg)
	if err != nil || rec != nil {
		t.Fatalf("absent record: %v, %v", rec, err)
	}

	endpoints := []RPCEndpoint{{URL: "https://base.example.org", Tracking: "none"}}
	if err := recordChainlistRPCs(cfg, 8453, "base", endpoints, true); err != nil {
		t.Fatal(err)
	}
	if err := recordCustomRPC(cfg, 8453, "base", "https://lb.drpc.live/base/secret-key", false); err != nil {
		t.Fatal(err)
	}
	if err := recordCustomRPC(cfg, 1, "ethereum", "https://eth.example.org", true); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(recordedRPCPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("record may hold paid RPC keys but has mode %v, want 0600", info.Mode().Perm())
	}

	rec, err = readRecordedRPCs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := rec.Chains["8453"]
	if base == nil || base.Name != "base" {
		t.Fatalf("base chain record missing: %+v", rec.Chains)
	}
	if base.Chainlist == nil || !base.Chainlist.ReadOnly || len(base.Chainlist.Endpoints) != 1 {
		t.Fatalf("chainlist record wrong: %+v", base.Chainlist)
	}
	if base.Custom == nil || base.Custom.ReadOnly || base.Custom.Endpoint != "https://lb.drpc.live/base/secret-key" {
		t.Fatalf("custom record wrong: %+v", base.Custom)
	}

	// Removing chainlist keeps the custom upstream for the same chain...
	if err := unrecordChainlistRPCs(cfg, 8453); err != nil {
		t.Fatal(err)
	}
	rec, _ = readRecordedRPCs(cfg)
	if rec.Chains["8453"] == nil || rec.Chains["8453"].Chainlist != nil || rec.Chains["8453"].Custom == nil {
		t.Fatalf("unrecord chainlist must keep custom: %+v", rec.Chains["8453"])
	}

	// ...and a chain with only a custom record is untouched by chainlist
	// removal of another chain. Removing a chain with neither drops the key.
	if err := unrecordChainlistRPCs(cfg, 1); err != nil {
		t.Fatal(err)
	}
	rec, _ = readRecordedRPCs(cfg)
	if rec.Chains["1"] == nil || rec.Chains["1"].Custom == nil {
		t.Fatalf("custom-only chain must survive chainlist unrecord: %+v", rec.Chains)
	}

	// Unrecording a never-recorded chain is a no-op.
	if err := unrecordChainlistRPCs(cfg, 42161); err != nil {
		t.Fatal(err)
	}
}
