// Package agentskills resolves the skills obol installs into Hermes agents:
// the stack-operation skills embedded in the binary, plus opt-in skills from a
// pinned ObolNetwork/skills release (installed with `obol agent skills add`).
package agentskills

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// Pinned ObolNetwork/skills release for opt-in skills. ContentSHA256 is the
// digest of the release's skills/ tree (see treeDigest), not of the tarball:
// GitHub archive bytes are not guaranteed stable, file contents are.
// Bump both together; `go test ./internal/agentskills -run TestPinnedRelease`
// with OBOL_SKILLS_VERIFY_PIN=1 prints the digest of the pinned tag.
const (
	Release       = "v1.8.0"
	ContentSHA256 = "45a34d5ac230bef54233114a78f3cb17f27e2977b7723f235bb3014965b73f06"
	repoTarball   = "https://codeload.github.com/ObolNetwork/skills/tar.gz/refs/tags/"
)

// SourceEnv points at a local ObolNetwork/skills checkout (or any directory
// with a skills/ subdirectory) to use instead of the pinned release, e.g.
// to try unreleased skill edits. No digest check is done on it.
const SourceEnv = "OBOL_SKILLS_SOURCE"

// repoSkillsDir returns the directory holding the release's skill folders,
// fetching and verifying the pinned release into the state dir on first use.
func repoSkillsDir(cfg *config.Config) (string, error) {
	if src := strings.TrimSpace(os.Getenv(SourceEnv)); src != "" {
		dir := filepath.Join(src, "skills")
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return "", fmt.Errorf("%s=%s has no skills/ directory", SourceEnv, src)
		}

		return dir, nil
	}

	if ContentSHA256 == "" {
		return "", fmt.Errorf("this obol build has no pinned ObolNetwork/skills release; set %s to a local checkout", SourceEnv)
	}

	cache := filepath.Join(cfg.StateDir, "skills", Release)
	dir := filepath.Join(cache, "skills")

	if digest, err := treeDigest(dir); err == nil && digest == ContentSHA256 {
		return dir, nil
	}

	if err := fetchRelease(cache, Release); err != nil {
		return "", err
	}

	digest, err := treeDigest(dir)
	if err != nil {
		return "", err
	}

	if digest != ContentSHA256 {
		_ = os.RemoveAll(cache)
		return "", fmt.Errorf("ObolNetwork/skills %s content digest %s does not match the pinned %s (refusing to install)", Release, digest, ContentSHA256)
	}

	return dir, nil
}

// fetchRelease downloads a release tarball of ObolNetwork/skills and extracts
// only its skills/ tree into cache/skills, replacing any previous copy.
func fetchRelease(cache, tag string) error {
	client := &http.Client{Timeout: 60 * time.Second}

	resp, err := client.Get(repoTarball + tag)
	if err != nil {
		return fmt.Errorf("download ObolNetwork/skills %s: %w", tag, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download ObolNetwork/skills %s: HTTP %d", tag, resp.StatusCode)
	}

	staging := cache + ".tmp"
	_ = os.RemoveAll(staging)

	if err := extractSkillsTree(resp.Body, filepath.Join(staging, "skills")); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}

	_ = os.RemoveAll(cache)
	if err := os.MkdirAll(filepath.Dir(cache), 0o755); err != nil {
		return err
	}

	return os.Rename(staging, cache)
}

// extractSkillsTree writes the `<root>/skills/...` entries of a GitHub repo
// tarball into dst. Only regular files and directories are written; paths are
// confined to dst.
func extractSkillsTree(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("read skills tarball: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	wrote := false

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return fmt.Errorf("read skills tarball: %w", err)
		}

		// GitHub archives prefix everything with "<repo>-<ref>/".
		_, rel, ok := strings.Cut(hdr.Name, "/")
		if !ok {
			continue
		}

		rel, ok = strings.CutPrefix(rel, "skills/")
		if !ok || rel == "" {
			continue
		}

		target := filepath.Join(dst, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, filepath.Clean(dst)+string(os.PathSeparator)) {
			return fmt.Errorf("skills tarball entry %q escapes the destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}

			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}

			if err := writeFrom(target, tr, mode); err != nil {
				return err
			}

			wrote = true
		}
	}

	if !wrote {
		return errors.New("skills tarball has no skills/ directory")
	}

	return nil
}

func writeFrom(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return err
	}

	return f.Close()
}

// treeDigest hashes every regular file under dir as "relpath\x00sha256\n",
// sorted by path, so the digest depends only on paths and contents.
func treeDigest(dir string) (string, error) {
	var lines []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.Type().IsRegular() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}

		sum := sha256.Sum256(data)
		lines = append(lines, filepath.ToSlash(rel)+"\x00"+hex.EncodeToString(sum[:])+"\n")

		return nil
	})
	if err != nil {
		return "", err
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no files under %s", dir)
	}

	sort.Strings(lines)

	h := sha256.New()
	for _, l := range lines {
		_, _ = io.WriteString(h, l)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
