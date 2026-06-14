package dataset

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStore_SaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	store := NewStore(path)

	want := State{
		ID:      "pi-sessions",
		GroupID: "grp-123",
		Versions: []DatasetVersion{
			{Seq: 1, ManifestHash: hashA, FileHash: hashA, Size: 100, Timestamp: fixedTime, Signature: "sig1"},
			{Seq: 2, ManifestHash: hashB, FileHash: hashB, Size: 200, PrevSig: "sig1", Timestamp: fixedTime, Signature: "sig2"},
		},
		Entitlements: []Entitlement{{TokenHash: "tok", GroupID: "grp-123", MaxVersion: 2, PaidAtomic: "2000"}},
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save: %v", err) // also creates the nested dir
	}
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestStore_LoadMissingFileIsEmpty(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "does-not-exist.json"))
	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load of missing file should not error, got %v", err)
	}
	if !reflect.DeepEqual(got, State{}) {
		t.Errorf("missing-file Load = %+v, want zero State", got)
	}
}

func TestStore_SaveOverwritesAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStore(path)

	if err := store.Save(State{ID: "v1", Versions: []DatasetVersion{{Seq: 1, ManifestHash: hashA, FileHash: hashA, Timestamp: fixedTime}}}); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	if err := store.Save(State{ID: "v2", Versions: []DatasetVersion{
		{Seq: 1, ManifestHash: hashA, FileHash: hashA, Timestamp: fixedTime},
		{Seq: 2, ManifestHash: hashB, FileHash: hashB, Timestamp: fixedTime.Add(time.Hour)},
	}}); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ID != "v2" || len(got.Versions) != 2 {
		t.Errorf("overwrite failed: got ID=%q versions=%d, want v2/2", got.ID, len(got.Versions))
	}
}
