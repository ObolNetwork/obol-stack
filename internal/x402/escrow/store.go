package escrow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StateAwaitingVoucher is the escrow entry state between a voucher-less
// Reserve (which surfaces the facilitator's spender address for the signer to
// bind) and the re-reserve that attaches the signed voucher.
const StateAwaitingVoucher = "AwaitingVoucher"

// Entry is one escrow's persisted lifecycle record.
type Entry struct {
	// ID is the escrow key (the ServiceBounty UID).
	ID string `json:"id"`
	// State is one of AwaitingVoucher | Reserved | Captured | Voided.
	State string `json:"state"`
	// Request is the last accepted reserve request, including the voucher.
	Request *ReserveRequest `json:"request,omitempty"`
	// Receipt is the receipt last returned for this entry.
	Receipt Receipt `json:"receipt"`
	// UpdatedAt is the last state-transition time.
	UpdatedAt time.Time `json:"updatedAt"`
}

// Store is a file-backed JSON escrow store: one <id>.json per entry, written
// via temp-file + atomic rename so a crash never leaves a torn entry.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore creates (if needed) and opens the state directory.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("escrow store: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("escrow store: create %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

// Get loads the entry for id; ok is false when none exists.
func (s *Store) Get(id string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("escrow store: read %s: %w", id, err)
	}
	var e Entry
	if err := json.Unmarshal(raw, &e); err != nil {
		return Entry{}, false, fmt.Errorf("escrow store: decode %s: %w", id, err)
	}
	return e, true, nil
}

// Put persists the entry atomically (write temp file, fsync, rename).
func (s *Store) Put(e Entry) error {
	if e.ID == "" {
		return fmt.Errorf("escrow store: entry has no id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("escrow store: encode %s: %w", e.ID, err)
	}

	tmp, err := os.CreateTemp(s.dir, "."+e.ID+".tmp-*")
	if err != nil {
		return fmt.Errorf("escrow store: temp file for %s: %w", e.ID, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("escrow store: write %s: %w", e.ID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("escrow store: sync %s: %w", e.ID, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("escrow store: close %s: %w", e.ID, err)
	}
	if err := os.Rename(tmpName, s.path(e.ID)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("escrow store: rename %s: %w", e.ID, err)
	}
	return nil
}
