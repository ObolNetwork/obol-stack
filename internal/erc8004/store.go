package erc8004

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNoRegistration is returned when no registration record exists on disk.
var ErrNoRegistration = errors.New("erc8004: no registration found")

// RegistrationRecord is the on-disk persistence of an ERC-8004 registration.
type RegistrationRecord struct {
	AgentID  string `json:"agentId"`
	AgentURI string `json:"agentUri"`
	TxHash   string `json:"txHash"`
	Chain    string `json:"chain"`
	Registry string `json:"registry"`
}

// Store manages ERC-8004 registration records on disk.
// Layout: <configDir>/x402/registration.json
type Store struct {
	root string
}

// NewStore creates a Store rooted at configDir.
func NewStore(configDir string) *Store {
	return &Store{root: filepath.Join(configDir, "x402")}
}

func (s *Store) path() string {
	return filepath.Join(s.root, "registration.json")
}

// Save persists a registration record.
func (s *Store) Save(rec *RegistrationRecord) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("erc8004 store: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("erc8004 store: marshal: %w", err)
	}
	if err := os.WriteFile(s.path(), data, 0o600); err != nil {
		return fmt.Errorf("erc8004 store: write: %w", err)
	}
	return nil
}

// Load reads the registration record from disk.
// Returns ErrNoRegistration if the file does not exist.
func (s *Store) Load() (*RegistrationRecord, error) {
	data, err := os.ReadFile(s.path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoRegistration
		}
		return nil, fmt.Errorf("erc8004 store: read: %w", err)
	}
	var rec RegistrationRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("erc8004 store: parse: %w", err)
	}
	return &rec, nil
}
