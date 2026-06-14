package dataset

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State is the restart-surviving state of a dataset server: its signed
// version log plus the member-token entitlements. Persisting the entitlements
// (by token hash) lets a continuously-sold dataset rehydrate paying members
// after a host restart instead of forcing every subscriber to re-pay.
type State struct {
	ID           string           `json:"id"`
	GroupID      string           `json:"groupID"`
	Versions     []DatasetVersion `json:"versions"`
	Entitlements []Entitlement    `json:"entitlements"`
}

// Store persists State to a JSON file using an atomic temp-file + rename, so a
// crash mid-write never leaves a torn file (matches internal/network/record.go).
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore returns a Store backed by path.
func NewStore(path string) *Store { return &Store{path: path} }

// Path is the backing file path.
func (s *Store) Path() string { return s.path }

// Load reads the persisted state. A missing file is not an error — it returns
// the zero State so a fresh dataset starts clean.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("dataset: read store %s: %w", s.path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("dataset: parse store %s: %w", s.path, err)
	}
	return st, nil
}

// Save atomically writes state: marshal -> temp file in the same dir -> fsync
// via close -> rename over the target.
func (s *Store) Save(st State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("dataset: marshal store: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("dataset: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".dataset-*.tmp")
	if err != nil {
		return fmt.Errorf("dataset: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("dataset: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("dataset: close temp: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("dataset: rename temp over %s: %w", s.path, err)
	}
	return nil
}
