package inference_test

import (
	"errors"
	"os"
	"runtime"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/inference"
)

func TestStoreCreateAndGet(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	d := &inference.Deployment{
		Name:          "test-deploy",
		WalletAddress: "0xdeadbeef",
	}

	if err := store.Create(d, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// EnclaveTag is auto-assigned only on macOS (Secure Enclave).
	// On Linux the field stays empty so the gateway runs without the
	// enclave middleware (payment gating still works).
	if runtime.GOOS == "darwin" {
		if d.EnclaveTag == "" {
			t.Error("EnclaveTag should have been set by Create on macOS")
		}
	} else {
		if d.EnclaveTag != "" {
			t.Errorf("EnclaveTag should be empty on %s, got %q", runtime.GOOS, d.EnclaveTag)
		}
	}

	if d.Chain == "" {
		t.Error("Chain should have been set by Create")
	}
	if d.Chain != "base" {
		t.Errorf("Chain = %q, want %q", d.Chain, "base")
	}

	if d.CreatedAt == "" {
		t.Error("CreatedAt should have been set by Create")
	}

	got, err := store.Get("test-deploy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Name != "test-deploy" {
		t.Errorf("Name mismatch: %s", got.Name)
	}

	if got.WalletAddress != "0xdeadbeef" {
		t.Errorf("WalletAddress mismatch: %s", got.WalletAddress)
	}

	if runtime.GOOS == "darwin" {
		if got.EnclaveTag != "com.obol.inference.test-deploy" {
			t.Errorf("unexpected EnclaveTag: %s", got.EnclaveTag)
		}
	} else {
		if got.EnclaveTag != "" {
			t.Errorf("EnclaveTag should be empty on %s, got %q", runtime.GOOS, got.EnclaveTag)
		}
	}
	if got.Chain != "base" {
		t.Errorf("persisted Chain = %q, want %q", got.Chain, "base")
	}
	if got.Chain != "base" {
		t.Errorf("persisted Chain = %q, want %q", got.Chain, "base")
	}
	if got.Chain != "base" {
		t.Errorf("persisted Chain = %q, want %q", got.Chain, "base")
	}
}

func TestStoreCreate_PersistsPerMTokMetadata(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	d := &inference.Deployment{
		Name:                   "priced",
		WalletAddress:          "0xdeadbeef",
		PricePerRequest:        "0.0005",
		PricePerMTok:           "0.50",
		ApproxTokensPerRequest: 1000,
	}

	if err := store.Create(d, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("priced")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.PricePerRequest != "0.0005" {
		t.Errorf("PricePerRequest = %q, want %q", got.PricePerRequest, "0.0005")
	}

	if got.PricePerMTok != "0.50" {
		t.Errorf("PricePerMTok = %q, want %q", got.PricePerMTok, "0.50")
	}

	if got.ApproxTokensPerRequest != 1000 {
		t.Errorf("ApproxTokensPerRequest = %d, want %d", got.ApproxTokensPerRequest, 1000)
	}
}

func TestStoreCreate_PersistsCanonicalProvenance(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	d := &inference.Deployment{
		Name:          "prov",
		WalletAddress: "0xdeadbeef",
		Provenance: &inference.Provenance{
			Framework:    "autoresearch",
			MetricName:   "val_bpb",
			MetricValue:  "0.9973",
			ExperimentID: "abc123",
			TrainHash:    "sha256:deadbeef",
			ParamCount:   "50000000",
		},
	}

	if err := store.Create(d, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.Get("prov")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Provenance == nil {
		t.Fatal("Provenance should be persisted")
	}

	if got.Provenance.Framework != "autoresearch" {
		t.Errorf("Framework = %q, want %q", got.Provenance.Framework, "autoresearch")
	}

	if got.Provenance.MetricName != "val_bpb" {
		t.Errorf("MetricName = %q, want %q", got.Provenance.MetricName, "val_bpb")
	}

	if got.Provenance.MetricValue != "0.9973" {
		t.Errorf("MetricValue = %q, want %q", got.Provenance.MetricValue, "0.9973")
	}

	if got.Provenance.ExperimentID != "abc123" {
		t.Errorf("ExperimentID = %q, want %q", got.Provenance.ExperimentID, "abc123")
	}

	if got.Provenance.TrainHash != "sha256:deadbeef" {
		t.Errorf("TrainHash = %q, want %q", got.Provenance.TrainHash, "sha256:deadbeef")
	}

	if got.Provenance.ParamCount != "50000000" {
		t.Errorf("ParamCount = %q, want %q", got.Provenance.ParamCount, "50000000")
	}
}

func TestStoreCreateDuplicate(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	d := &inference.Deployment{Name: "dup"}
	if err := store.Create(d, false); err != nil {
		t.Fatalf("first Create: %v", err)
	}

	// Second create without force should fail.
	err := store.Create(&inference.Deployment{Name: "dup"}, false)
	if !errors.Is(err, inference.ErrDeploymentExists) {
		t.Fatalf("expected ErrDeploymentExists, got %v", err)
	}

	// With force=true it should succeed.
	if err := store.Create(&inference.Deployment{Name: "dup"}, true); err != nil {
		t.Fatalf("forced Create: %v", err)
	}
}

func TestStoreList(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	names := []string{"alpha", "beta", "gamma"}
	for _, n := range names {
		if err := store.Create(&inference.Deployment{Name: n}, false); err != nil {
			t.Fatalf("Create %s: %v", n, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list) != len(names) {
		t.Fatalf("List returned %d deployments, want %d", len(list), len(names))
	}
}

func TestStoreListEmpty(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	list, err := store.List()
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}

	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d items", len(list))
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	if err := store.Create(&inference.Deployment{Name: "todelete"}, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete("todelete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := store.Get("todelete"); !errors.Is(err, inference.ErrDeploymentNotFound) {
		t.Fatalf("expected ErrDeploymentNotFound after delete, got %v", err)
	}
}

func TestStoreGetNotFound(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	_, err := store.Get("nonexistent")
	if !errors.Is(err, inference.ErrDeploymentNotFound) {
		t.Fatalf("expected ErrDeploymentNotFound, got %v", err)
	}
}

func TestStoreUpdate(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	d := &inference.Deployment{Name: "upd", Chain: "base-sepolia"}
	if err := store.Create(d, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	d.Chain = "polygon"
	if err := store.Update(d); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.Get("upd")
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}

	if got.Chain != "polygon" {
		t.Errorf("Chain not updated: %s", got.Chain)
	}

	if got.UpdatedAt == "" {
		t.Error("UpdatedAt should be set after Update")
	}
}

// TestStoreDirPermissions verifies that config files are written with
// restricted permissions (owner-only).
func TestStoreDirPermissions(t *testing.T) {
	dir := t.TempDir()
	store := inference.NewStore(dir)

	if err := store.Create(&inference.Deployment{Name: "perm-test"}, false); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Config file should be 0600.
	cfgPath := dir + "/inference/perm-test/config.json"

	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}

	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config.json permissions: want 0600, got %04o", mode)
	}
}
