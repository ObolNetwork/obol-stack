package dataset

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Artifacts resolves the raw bytes of a dataset version for serving. The
// returned reader is seekable so the HTTP layer (http.ServeContent) can honor
// Range requests; the caller closes it.
type Artifacts interface {
	Open(version int) (content io.ReadSeeker, modtime time.Time, closeFn func() error, err error)
}

// FileArtifacts serves each version from a file on disk. Versions are
// registered as they are published (Set), so a snapshot's bytes stay pinned
// to the path that priced and signed it.
type FileArtifacts struct {
	mu    sync.RWMutex
	paths map[int]string
}

// NewFileArtifacts returns an empty file-backed artifact store.
func NewFileArtifacts() *FileArtifacts {
	return &FileArtifacts{paths: map[int]string{}}
}

// Set registers the file path serving a version.
func (f *FileArtifacts) Set(version int, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths[version] = path
}

// Open implements Artifacts.
func (f *FileArtifacts) Open(version int) (io.ReadSeeker, time.Time, func() error, error) {
	f.mu.RLock()
	path, ok := f.paths[version]
	f.mu.RUnlock()
	if !ok {
		return nil, time.Time{}, nil, fmt.Errorf("dataset: no artifact registered for version %d", version)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("dataset: open artifact v%d: %w", version, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, time.Time{}, nil, fmt.Errorf("dataset: stat artifact v%d: %w", version, err)
	}
	return file, info.ModTime(), file.Close, nil
}
