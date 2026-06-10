package stackbackup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// archiveWriter streams a tar.gz to disk. Trees are added straight from
// their source roots (no staging copy) so multi-GB agent data dirs are not
// duplicated on disk during export.
type archiveWriter struct {
	f  *os.File
	gz *gzip.Writer
	tw *tar.Writer
}

func newArchiveWriter(path string) (*archiveWriter, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create archive: %w", err)
	}
	gz := gzip.NewWriter(f)
	return &archiveWriter{f: f, gz: gz, tw: tar.NewWriter(gz)}, nil
}

func (w *archiveWriter) Close() error {
	terr := w.tw.Close()
	gerr := w.gz.Close()
	ferr := w.f.Close()
	return errors.Join(terr, gerr, ferr)
}

// addBytes writes a single regular file entry.
func (w *archiveWriter) addBytes(name string, data []byte, mode int64) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}
	if err := w.tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := w.tw.Write(data)
	return err
}

// addTree walks srcDir and adds every entry under prefix (e.g. "config").
// skip filters by srcDir-relative path; symlinks are preserved as symlinks;
// irregular files (sockets, devices) and unreadable entries are skipped with
// a warning rather than failing the whole export.
func (w *archiveWriter) addTree(srcDir, prefix string, skip func(rel string) bool) (warnings []string, err error) {
	err = filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, walkErr))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, infoErr))
			return nil
		}

		name := prefix + "/" + filepath.ToSlash(rel)
		switch {
		case info.Mode().IsDir():
			hdr, _ := tar.FileInfoHeader(info, "")
			hdr.Name = name + "/"
			return w.tw.WriteHeader(hdr)
		case info.Mode()&os.ModeSymlink != 0:
			target, linkErr := os.Readlink(path)
			if linkErr != nil {
				warnings = append(warnings, fmt.Sprintf("skipping symlink %s: %v", path, linkErr))
				return nil
			}
			hdr, _ := tar.FileInfoHeader(info, target)
			hdr.Name = name
			return w.tw.WriteHeader(hdr)
		case info.Mode().IsRegular():
			f, openErr := os.Open(path)
			if openErr != nil {
				warnings = append(warnings, fmt.Sprintf("skipping %s: %v", path, openErr))
				return nil
			}
			defer f.Close()
			hdr, _ := tar.FileInfoHeader(info, "")
			hdr.Name = name
			if err := w.tw.WriteHeader(hdr); err != nil {
				return err
			}
			_, copyErr := io.Copy(w.tw, f)
			return copyErr
		default:
			warnings = append(warnings, fmt.Sprintf("skipping irregular file %s (%s)", path, info.Mode()))
			return nil
		}
	})
	return warnings, err
}

// readArchiveManifest opens the archive and returns its manifest without
// extracting anything else. The manifest is written as the first entry, but
// scan the whole stream defensively in case of re-packed archives.
func readArchiveManifest(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("manifest.json not found in archive — not an obol stack export?")
		}
		if err != nil {
			return nil, err
		}
		if filepath.Clean(hdr.Name) == ManifestFileName {
			data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, err
			}
			return decodeManifest(data)
		}
	}
}

// sanitizeEntryName rejects absolute and path-escaping tar entry names and
// returns the cleaned relative path.
func sanitizeEntryName(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes extraction root: %q", name)
	}
	return clean, nil
}

// extractEntry writes one tar entry under destRoot/relName.
func extractEntry(tr *tar.Reader, hdr *tar.Header, destRoot, relName string) error {
	dest := filepath.Join(destRoot, relName)
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(dest, os.FileMode(hdr.Mode)|0o700)
	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		_ = os.Remove(dest)
		return os.Symlink(hdr.Linkname, dest)
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	default:
		return nil // ignore other entry types
	}
}
