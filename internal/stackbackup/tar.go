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

// symlinkEscapesRoot reports whether a symlink placed at linkPath pointing to
// target would resolve outside destRoot. Entry NAMES are sanitized
// (sanitizeEntryName), but a symlink's TARGET is not — an unchecked target
// lets a LATER entry be written THROUGH the link to an arbitrary path (the
// classic symlink tar-extraction escape: entry "x" -> /etc, then entry
// "x/passwd" resolves outside the root). Absolute targets and ".."-walking
// relative targets that leave the root are rejected; in-root links (the common
// case — a relative link to a sibling file) are allowed.
func symlinkEscapesRoot(destRoot, linkPath, target string) bool {
	var resolved string
	if filepath.IsAbs(target) {
		resolved = filepath.Clean(target)
	} else {
		resolved = filepath.Clean(filepath.Join(filepath.Dir(linkPath), target))
	}
	root := filepath.Clean(destRoot)
	return resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator))
}

// extractEntry writes one tar entry under destRoot/relName.
func extractEntry(tr *tar.Reader, hdr *tar.Header, destRoot, relName string) error {
	dest := filepath.Join(destRoot, relName)
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(dest, os.FileMode(hdr.Mode)|0o700)
	case tar.TypeSymlink:
		if symlinkEscapesRoot(destRoot, dest, hdr.Linkname) {
			return fmt.Errorf("symlink %q targets %q which escapes extraction root", relName, hdr.Linkname)
		}
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

// Decompression-bomb guard. archive/tar bounds each file read to the header
// size, but a tiny gzip can declare (and inflate to) a disk-filling tar. The
// guard tracks the live ratio of uncompressed bytes produced to compressed
// bytes consumed and aborts once it is implausible. bombFloorBytes avoids
// false positives on small, naturally high-ratio inputs; legitimate multi-GB
// agent-data exports never sustain a ratio this high. Both are vars so tests
// can lower them. (vars, not consts, for that reason.)
var (
	bombFloorBytes      int64 = 64 << 20 // ignore the ratio below this many uncompressed bytes
	maxCompressionRatio int64 = 100      // abort above this uncompressed:compressed ratio
)

// countingReader counts the bytes read from an underlying reader (used on the
// raw compressed stream, beneath gzip).
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// ratioGuard wraps the decompressed stream and trips when the running
// decompression ratio exceeds maxCompressionRatio past bombFloorBytes. It
// sits between gzip and tar, so every byte the tar reader consumes — headers
// and file bodies alike, including io.Copy in extractEntry — is accounted.
type ratioGuard struct {
	r            io.Reader
	compressed   *int64 // bytes pulled from the compressed source so far
	uncompressed int64
}

func (g *ratioGuard) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	g.uncompressed += int64(n)
	if g.uncompressed > bombFloorBytes {
		if c := *g.compressed; c > 0 && g.uncompressed/c > maxCompressionRatio {
			return n, fmt.Errorf("refusing archive: decompression ratio %d:1 exceeds %d:1 limit (possible decompression bomb)", g.uncompressed/c, maxCompressionRatio)
		}
	}
	return n, err
}
