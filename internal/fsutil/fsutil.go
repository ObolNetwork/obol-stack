// Package fsutil holds small filesystem helpers shared across the CLI.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// SecretFileMode is the permission every file holding key material,
// passwords or tokens ends up with.
const SecretFileMode os.FileMode = 0o600

// SecretDirMode is the permission used for parent directories created on
// demand by WriteSecret.
const SecretDirMode os.FileMode = 0o700

// WriteSecret atomically writes data to path with mode 0600.
//
// Unlike os.WriteFile, the resulting mode does not depend on whether the
// file already existed (os.WriteFile keeps an existing file's permissions)
// or on the process umask. The data is written to a temp file in the same
// directory, synced, chmod'ed to 0600 and renamed over the target, so
// readers never observe a partially written file. Missing parent
// directories are created with mode 0700; existing ones are left as-is.
func WriteSecret(path string, data []byte) error {
	name := filepath.Base(path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, SecretDirMode); err != nil {
		return fmt.Errorf("create directory for %s: %w", name, err)
	}

	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", name, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	// CreateTemp already uses 0600; set it explicitly so the result never
	// depends on platform defaults or the umask.
	if err := tmp.Chmod(SecretFileMode); err != nil {
		return fmt.Errorf("chmod temp file for %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}
	committed = true

	syncDir(dir)
	return nil
}

// syncDir fsyncs a directory so a preceding rename is durable. Best-effort:
// not every platform/filesystem supports syncing a directory.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
