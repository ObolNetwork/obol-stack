package inference_test

import (
	"errors"
	"os"
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

	// Defaults should be applied.
	if d.EnclaveTag == "" {
		t.Error("EnclaveTag should have been set by Create")
	}
	if d.Chain == "" {
		t.Error("Chain should have been set by Create")
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
	if got.EnclaveTag != "com.obol.inference.test-deploy" {
		t.Errorf("unexpected EnclaveTag: %s", got.EnclaveTag)
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
