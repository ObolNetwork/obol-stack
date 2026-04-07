package buyer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// StateStore tracks consumed authorization nonces so hot reloads and restarts
// do not reintroduce already-spent vouchers from the ConfigMap source.
type StateStore struct {
	mu       sync.RWMutex
	path     string
	consumed map[string]map[string]struct{}
}

type stateFile struct {
	Consumed map[string][]string `json:"consumed"`
}

// LoadStateStore loads consumed authorization state from disk. Missing files
// are treated as an empty state.
func LoadStateStore(path string) (*StateStore, error) {
	store := &StateStore{
		path:     path,
		consumed: make(map[string]map[string]struct{}),
	}
	if path == "" {
		return store, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}

		return nil, fmt.Errorf("read state %s: %w", path, err)
	}

	var decoded stateFile
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}

	for upstream, nonces := range decoded.Consumed {
		nonceSet := make(map[string]struct{}, len(nonces))
		for _, nonce := range nonces {
			nonceSet[nonce] = struct{}{}
		}

		store.consumed[upstream] = nonceSet
	}

	return store, nil
}

// IsConsumed reports whether a nonce was already consumed for an upstream.
func (s *StateStore) IsConsumed(upstream, nonce string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.consumed[upstream][nonce]

	return ok
}

// ConsumedCount returns the number of consumed authorizations for an upstream.
func (s *StateStore) ConsumedCount(upstream string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.consumed[upstream])
}

// MarkConsumed records a consumed authorization nonce and persists the updated
// state to disk.
func (s *StateStore) MarkConsumed(upstream, nonce string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.consumed[upstream] == nil {
		s.consumed[upstream] = make(map[string]struct{})
	}

	if _, ok := s.consumed[upstream][nonce]; ok {
		return nil
	}

	s.consumed[upstream][nonce] = struct{}{}

	return s.writeLocked()
}

func (s *StateStore) writeLocked() error {
	if s.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}

	out := stateFile{Consumed: make(map[string][]string, len(s.consumed))}
	for upstream, nonceSet := range s.consumed {
		nonces := make([]string, 0, len(nonceSet))
		for nonce := range nonceSet {
			nonces = append(nonces, nonce)
		}

		out.Consumed[upstream] = nonces
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write temp state: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}

	return nil
}
