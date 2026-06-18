// Package skillpkg packages a skill directory (SKILL.md + scripts, the
// same shape as internal/embed/skills/*) into a byte-for-byte
// deterministic gzipped tarball so the sha256 of the artifact is a
// stable identity for the skill content. The hash is what `obol sell
// skill` pins into the ServiceOffer spec and what `obol skills calldata
// set-hash` anchors on the ERC-8004 Identity Registry, so two packs of
// the same content MUST produce identical bytes regardless of file
// mtimes, ownership, umask, or on-disk creation order.
package skillpkg

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
)

const (
	// MaxBundleBytes caps the gzipped bundle size. It mirrors
	// monetizeapi.MaxSkillBundleBytes (asserted equal in tests): the
	// artifact rides a ConfigMap (1MiB object cap) and must leave room
	// for base64 expansion plus object metadata, so the cap applies to
	// the compressed bytes. Pack enforces it so no caller can persist
	// an artifact the controller would refuse to publish.
	MaxBundleBytes = 900000

	// ManifestName is the required top-level file. A skill bundle
	// without SKILL.md is not a skill.
	ManifestName = "SKILL.md"
)

// entry is one path collected from the source tree, pre-sorted and
// pre-classified so the tar emission loop is trivially deterministic.
type entry struct {
	path string // slash-separated, relative to root
	dir  bool
	exec bool // any exec bit set on the source file
}

// Pack walks root, packs every regular file and directory into a
// deterministic USTAR tar wrapped in a deterministic gzip stream, and
// returns the compressed bytes plus their lowercase hex sha256.
//
// Determinism rules:
//   - entries sorted lexicographically by slash-separated path
//   - file modes normalized to 0644 (0755 when any source exec bit is
//     set); directory modes normalized to 0755
//   - ModTime fixed to the Unix epoch; uid/gid 0; uname/gname cleared
//   - gzip header carries no name, zero mtime, and OS byte 255
//
// Symlinks and irregular files are rejected (a bundle must be fully
// self-contained and portable); __pycache__ directories and *.pyc files
// are skipped, mirroring embed.WriteSkillSubset. The gzipped result is
// rejected when it exceeds MaxBundleBytes.
func Pack(root fs.FS) ([]byte, string, error) {
	entries, err := collectEntries(root)
	if err != nil {
		return nil, "", err
	}

	if !hasTopLevelManifest(entries) {
		return nil, "", fmt.Errorf("skillpkg: bundle root must contain %s — a skill bundle without %s is not a skill", ManifestName, ManifestName)
	}

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, "", fmt.Errorf("skillpkg: gzip writer: %w", err)
	}
	// Deterministic gzip header: no original name, zero mtime (written
	// as 0), and an explicit "unknown" OS byte so the output does not
	// vary across platforms or Go releases.
	zw.Header.Name = ""
	zw.Header.ModTime = time.Time{}
	zw.Header.OS = 255

	tw := tar.NewWriter(zw)
	for _, e := range entries {
		if e.dir {
			if err := tw.WriteHeader(dirHeader(e.path)); err != nil {
				return nil, "", fmt.Errorf("skillpkg: write dir header %s: %w", e.path, err)
			}
			continue
		}
		data, err := fs.ReadFile(root, e.path)
		if err != nil {
			return nil, "", fmt.Errorf("skillpkg: read %s: %w", e.path, err)
		}
		if err := tw.WriteHeader(fileHeader(e.path, int64(len(data)), e.exec)); err != nil {
			return nil, "", fmt.Errorf("skillpkg: write file header %s: %w", e.path, err)
		}
		if _, err := tw.Write(data); err != nil {
			return nil, "", fmt.Errorf("skillpkg: write %s: %w", e.path, err)
		}
	}
	if err := tw.Close(); err != nil {
		return nil, "", fmt.Errorf("skillpkg: close tar: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, "", fmt.Errorf("skillpkg: close gzip: %w", err)
	}

	gz := buf.Bytes()
	if len(gz) > MaxBundleBytes {
		return nil, "", fmt.Errorf("skillpkg: gzipped bundle is %d bytes, which exceeds the %d-byte skill bundle cap (the artifact must fit in a ConfigMap) — trim large assets from the skill directory", len(gz), MaxBundleBytes)
	}

	sum := sha256.Sum256(gz)
	return gz, hex.EncodeToString(sum[:]), nil
}

// ScanSecrets walks root with the same entry rules as Pack and returns
// one human-readable warning per entry that looks like it carries
// secret material: .env-style files, id_rsa* key files, and any file
// whose content carries a PEM "PRIVATE KEY" marker. Warn-only by
// contract — callers print the warnings and proceed; a skill author may
// legitimately ship an .env.example.
func ScanSecrets(root fs.FS) ([]string, error) {
	entries, err := collectEntries(root)
	if err != nil {
		return nil, err
	}

	var warnings []string
	for _, e := range entries {
		if e.dir {
			continue
		}
		base := path.Base(e.path)
		switch {
		case base == ".env" || strings.HasPrefix(base, ".env."):
			warnings = append(warnings, fmt.Sprintf("%s: looks like an environment file — it will be published to every buyer", e.path))
		case strings.HasPrefix(base, "id_rsa"):
			warnings = append(warnings, fmt.Sprintf("%s: looks like an SSH key file — it will be published to every buyer", e.path))
		}
		data, err := fs.ReadFile(root, e.path)
		if err != nil {
			return nil, fmt.Errorf("skillpkg: read %s: %w", e.path, err)
		}
		if bytes.Contains(data, []byte("PRIVATE KEY")) {
			warnings = append(warnings, fmt.Sprintf("%s: contains a PEM \"PRIVATE KEY\" marker — it will be published to every buyer", e.path))
		}
	}
	return warnings, nil
}

// collectEntries walks root and returns the full, lexicographically
// sorted entry list. Symlinks and other irregular files error out;
// __pycache__ dirs and *.pyc files are skipped (they are interpreter
// artifacts that vary per machine and would break hash determinism).
func collectEntries(root fs.FS) ([]entry, error) {
	var entries []entry
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return fs.SkipDir
			}
			entries = append(entries, entry{path: p, dir: true})
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("skillpkg: unsupported entry %q (%s): symlinks and special files cannot be packed into a skill bundle", p, d.Type())
		}
		if strings.HasSuffix(d.Name(), ".pyc") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("skillpkg: stat %s: %w", p, err)
		}
		entries = append(entries, entry{path: p, exec: info.Mode().Perm()&0o111 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// One sorted order for everything. Parents naturally precede their
	// children ("a" < "a/b"), so extraction order is always valid.
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries, nil
}

func hasTopLevelManifest(entries []entry) bool {
	for _, e := range entries {
		if !e.dir && e.path == ManifestName {
			return true
		}
	}
	return false
}

// dirHeader builds the normalized tar header for a directory entry.
func dirHeader(p string) *tar.Header {
	hdr := baseHeader(p + "/")
	hdr.Typeflag = tar.TypeDir
	hdr.Mode = 0o755
	return hdr
}

// fileHeader builds the normalized tar header for a regular file.
func fileHeader(p string, size int64, exec bool) *tar.Header {
	hdr := baseHeader(p)
	hdr.Typeflag = tar.TypeReg
	hdr.Size = size
	hdr.Mode = 0o644
	if exec {
		hdr.Mode = 0o755
	}
	return hdr
}

// baseHeader carries every normalized field shared by files and dirs:
// USTAR format, epoch mtime, zero atime/ctime, uid/gid 0, cleared
// uname/gname, forward-slash relative name.
func baseHeader(name string) *tar.Header {
	return &tar.Header{
		Name:       name,
		Format:     tar.FormatUSTAR,
		ModTime:    time.Unix(0, 0),
		AccessTime: time.Time{},
		ChangeTime: time.Time{},
		Uid:        0,
		Gid:        0,
		Uname:      "",
		Gname:      "",
	}
}
