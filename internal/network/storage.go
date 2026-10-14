package network

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StorageStatus is the on-disk size of a node's data (local-path PVCs live
// under <DataDir>/<namespace>/<pvc>) and the free space next to it.
type StorageStatus struct {
	Path           string  `json:"path"`
	ExecutionBytes uint64  `json:"execution_bytes"`
	ConsensusBytes uint64  `json:"consensus_bytes"`
	FreeBytes      *uint64 `json:"free_bytes,omitempty"`
	// Partial means some of it couldn't be read (permissions) or summed in
	// time; the sizes are then lower bounds.
	Partial bool `json:"partial,omitempty"`
}

// TotalBytes is execution + consensus data.
func (s *StorageStatus) TotalBytes() uint64 { return s.ExecutionBytes + s.ConsensusBytes }

// storageTTL bounds how often --watch re-walks a node's data directory.
const storageTTL = time.Minute

type storageCacheEntry struct {
	at time.Time
	st *StorageStatus
}

// storage measures d's data directory, cached for storageTTL so a watch
// session doesn't walk hundreds of GiB every tick. Nil when unknown.
func (p *NodeProber) storage(ctx context.Context, d Deployment) *StorageStatus {
	if p.DiskUsage == nil || p.DataDir == "" {
		return nil
	}

	now := p.now()

	p.storeMu.Lock()
	if e, ok := p.stores[d.Namespace]; ok && now.Sub(e.at) < storageTTL {
		p.storeMu.Unlock()
		return e.st
	}
	p.storeMu.Unlock()

	root := filepath.Join(p.DataDir, d.Namespace)

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	st := &StorageStatus{Path: root}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		n, partial, err := p.DiskUsage(ctx, filepath.Join(root, e.Name()))
		if err != nil || partial {
			st.Partial = true
		}

		switch {
		case strings.HasPrefix(e.Name(), "execution-"):
			st.ExecutionBytes += n
		case strings.HasPrefix(e.Name(), "consensus-"):
			st.ConsensusBytes += n
		}
	}

	if p.FreeBytes != nil {
		if free, err := p.FreeBytes(root); err == nil {
			st.FreeBytes = &free
		}
	}

	p.storeMu.Lock()
	if p.stores == nil {
		p.stores = map[string]storageCacheEntry{}
	}
	p.stores[d.Namespace] = storageCacheEntry{at: now, st: st}
	p.storeMu.Unlock()

	return st
}

// dirUsage sums file sizes under dir. Unreadable entries and a context
// that expires mid-walk make the result partial, never an error.
func dirUsage(ctx context.Context, dir string) (uint64, bool, error) {
	var (
		total   uint64
		partial bool
	)

	err := filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			partial = true
			if e != nil && e.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if ctx.Err() != nil {
			partial = true
			return fs.SkipAll
		}

		if e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				total += uint64(info.Size())
			} else {
				partial = true
			}
		}

		return nil
	})

	return total, partial, err
}
