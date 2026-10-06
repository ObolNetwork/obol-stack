package tools

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// maxDownloadBytes bounds a single tool download (helm-diff is the largest at
// ~25MB compressed).
const maxDownloadBytes = 512 << 20

// Installer downloads, verifies and installs manifest tools into BinDir.
type Installer struct {
	BinDir   string
	Manifest *Manifest
	Client   *http.Client
	GOOS     string
	GOARCH   string
	// PluginsDir overrides `helm env HELM_PLUGINS` discovery (tests).
	PluginsDir string
}

// NewInstaller returns an installer for the host platform using the
// embedded manifest.
func NewInstaller(binDir string) *Installer {
	goos, goarch := hostPlatform()

	return &Installer{
		BinDir:   binDir,
		Manifest: Default(),
		Client:   &http.Client{Timeout: 10 * time.Minute},
		GOOS:     goos,
		GOARCH:   goarch,
	}
}

// Install downloads and installs the pinned version of name, replacing any
// managed copy. It never touches binaries outside BinDir (helm plugins go to
// helm's plugin dir).
func (in *Installer) Install(ctx context.Context, name string) error {
	tool, ok := in.Manifest.Get(name)
	if !ok {
		return fmt.Errorf("unknown tool %q", name)
	}

	asset, err := tool.AssetFor(in.GOOS, in.GOARCH)
	if err != nil {
		return err
	}

	if tool.IsPlugin() {
		return in.installHelmPlugin(ctx, tool, asset)
	}

	if err := os.MkdirAll(in.BinDir, 0o755); err != nil {
		return fmt.Errorf("create bin dir: %w", err)
	}

	// Stage inside BinDir so the final rename is atomic (same filesystem).
	tmp, err := os.MkdirTemp(in.BinDir, ".obol-install-"+name+"-")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	dl := filepath.Join(tmp, "download")
	if err := in.download(ctx, asset.URL, dl, asset.SHA256); err != nil {
		return fmt.Errorf("%s %s: %w", name, tool.Version, err)
	}

	staged := dl
	if tool.Format != FormatRaw {
		staged = filepath.Join(tmp, name)
		if err := extractFile(tool.Format, dl, asset.Member, staged); err != nil {
			return fmt.Errorf("%s %s: %w", name, tool.Version, err)
		}
	}

	if err := os.Chmod(staged, 0o755); err != nil {
		return err
	}

	if err := os.Rename(staged, filepath.Join(in.BinDir, name)); err != nil {
		return fmt.Errorf("install %s: %w", name, err)
	}

	return nil
}

// download fetches url into dst and verifies its sha256. On mismatch dst is
// removed and an error returned: verification fails closed.
func (in *Installer) download(ctx context.Context, url, dst, wantSHA string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := in.Client
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}

	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxDownloadBytes+1))
	closeErr := f.Close()

	if err == nil {
		err = closeErr
	}

	if err == nil && n > maxDownloadBytes {
		err = fmt.Errorf("download %s: exceeds %d bytes", url, maxDownloadBytes)
	}

	if err != nil {
		os.Remove(dst)
		return fmt.Errorf("download %s: %w", url, err)
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, wantSHA) {
		os.Remove(dst)
		return fmt.Errorf("checksum mismatch for %s: expected sha256 %s, got %s (refusing to install)", url, wantSHA, got)
	}

	return nil
}

// --- archives --------------------------------------------------------------

func normMember(name string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, `\`, "/")), "/")
}

// archiveEntry is a regular file or directory read from an archive.
type archiveEntry struct {
	name  string // normalised, relative
	mode  os.FileMode
	isDir bool
	open  func() (io.ReadCloser, error)
}

func walkArchive(format, archivePath string, fn func(archiveEntry) error) error {
	switch format {
	case FormatTarGz:
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()

		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("read gzip: %w", err)
		}
		defer gz.Close()

		tr := tar.NewReader(gz)

		for {
			hdr, err := tr.Next()
			if errors.Is(err, io.EOF) {
				return nil
			}

			if err != nil {
				return fmt.Errorf("read tar: %w", err)
			}

			var e archiveEntry

			switch hdr.Typeflag {
			case tar.TypeReg:
				e = archiveEntry{name: normMember(hdr.Name), mode: hdr.FileInfo().Mode().Perm(), open: func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }}
			case tar.TypeDir:
				e = archiveEntry{name: normMember(hdr.Name), isDir: true}
			default:
				continue // symlinks, hardlinks, devices: never materialised
			}

			if err := fn(e); err != nil {
				return err
			}
		}
	case FormatZip:
		zr, err := zip.OpenReader(archivePath)
		if err != nil {
			return fmt.Errorf("read zip: %w", err)
		}
		defer zr.Close()

		for _, zf := range zr.File {
			mode := zf.Mode()

			var e archiveEntry

			switch {
			case mode.IsDir():
				e = archiveEntry{name: normMember(zf.Name), isDir: true}
			case mode.IsRegular():
				e = archiveEntry{name: normMember(zf.Name), mode: mode.Perm(), open: zf.Open}
			default:
				continue
			}

			if err := fn(e); err != nil {
				return err
			}
		}

		return nil
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}
}

var errFound = errors.New("found")

// extractFile writes the single archive member to dst.
func extractFile(format, archivePath, member, dst string) error {
	want := normMember(member)

	err := walkArchive(format, archivePath, func(e archiveEntry) error {
		if e.isDir || e.name != want {
			return nil
		}

		if err := writeEntry(e, dst, 0o755); err != nil {
			return err
		}

		return errFound
	})
	if errors.Is(err, errFound) {
		return nil
	}

	if err != nil {
		return err
	}

	return fmt.Errorf("archive member %q not found", member)
}

// extractDir extracts every regular file under prefix/ into dstDir,
// stripping the prefix. Entries escaping dstDir are rejected.
func extractDir(format, archivePath, prefix, dstDir string) error {
	pfx := normMember(prefix) + "/"
	count := 0

	err := walkArchive(format, archivePath, func(e archiveEntry) error {
		if e.isDir || !strings.HasPrefix(e.name, pfx) {
			return nil
		}

		rel := strings.TrimPrefix(e.name, pfx)
		target := filepath.Join(dstDir, filepath.FromSlash(rel))

		if !strings.HasPrefix(target, filepath.Clean(dstDir)+string(os.PathSeparator)) {
			return fmt.Errorf("archive entry %q escapes destination", e.name)
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}

		count++

		return writeEntry(e, target, 0)
	})
	if err != nil {
		return err
	}

	if count == 0 {
		return fmt.Errorf("archive has no entries under %q", prefix)
	}

	return nil
}

func writeEntry(e archiveEntry, dst string, forceMode os.FileMode) error {
	rc, err := e.open()
	if err != nil {
		return err
	}
	defer rc.Close()

	mode := e.mode
	if forceMode != 0 {
		mode = forceMode
	}

	if mode == 0 {
		mode = 0o644
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, io.LimitReader(rc, maxDownloadBytes)); err != nil {
		f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

// --- helm plugins ----------------------------------------------------------

// PluginInfo describes an installed helm plugin.
type PluginInfo struct {
	Dir     string
	Name    string
	Version string
}

// HelmPluginsDir returns the plugin directory helm uses (`helm env
// HELM_PLUGINS`), honouring HELM_PLUGINS / HELM_DATA_HOME like helm does.
// This is the same directory obolup.sh's `helm plugin install` writes to.
func (in *Installer) HelmPluginsDir() (string, error) {
	if in.PluginsDir != "" {
		return in.PluginsDir, nil
	}

	r := Resolve(in.BinDir, "helm")
	if !r.Found() {
		return "", MissingError("helm")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, r.Path, "env", "HELM_PLUGINS").Output()
	if err != nil {
		return "", fmt.Errorf("%s env HELM_PLUGINS: %w", r.Path, err)
	}

	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("helm reported an empty HELM_PLUGINS directory")
	}

	// HELM_PLUGINS may be a path list; helm installs into the first entry.
	return filepath.SplitList(dir)[0], nil
}

// FindPlugins lists plugins in dir whose plugin.yaml name equals name.
func FindPlugins(dir, name string) []PluginInfo {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var out []PluginInfo

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		pdir := filepath.Join(dir, e.Name())

		raw, err := os.ReadFile(filepath.Join(pdir, "plugin.yaml"))
		if err != nil {
			continue
		}

		var meta struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		}

		if yaml.Unmarshal(raw, &meta) != nil || meta.Name != name {
			continue
		}

		out = append(out, PluginInfo{Dir: pdir, Name: meta.Name, Version: meta.Version})
	}

	return out
}

func (in *Installer) installHelmPlugin(ctx context.Context, tool Tool, asset Asset) error {
	pluginsDir, err := in.HelmPluginsDir()
	if err != nil {
		return fmt.Errorf("%s: %w", tool.Name, err)
	}

	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return err
	}

	// Staging dir lives inside the plugins dir (same fs → atomic rename). It
	// is dot-prefixed and its plugin.yaml sits one level deeper, so helm never
	// loads it as a plugin.
	tmp, err := os.MkdirTemp(pluginsDir, ".obol-install-"+tool.Name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	dl := filepath.Join(tmp, "download")
	if err := in.download(ctx, asset.URL, dl, asset.SHA256); err != nil {
		return fmt.Errorf("%s %s: %w", tool.Name, tool.Version, err)
	}

	staged := filepath.Join(tmp, "plugin")
	if err := extractDir(tool.Format, dl, asset.Member, staged); err != nil {
		return fmt.Errorf("%s %s: %w", tool.Name, tool.Version, err)
	}

	if _, err := os.Stat(filepath.Join(staged, "plugin.yaml")); err != nil {
		return fmt.Errorf("%s %s: archive has no plugin.yaml", tool.Name, tool.Version)
	}

	// Move existing copies (any dir whose plugin.yaml claims the same name)
	// aside so helm never sees two plugins with one name, then swap in.
	target := filepath.Join(pluginsDir, tool.Name)
	existing := FindPlugins(pluginsDir, tool.PluginName)

	if len(existing) == 1 {
		target = existing[0].Dir
	}

	var moved [][2]string

	for i, p := range existing {
		aside := filepath.Join(tmp, fmt.Sprintf("old-%d", i))
		if err := os.Rename(p.Dir, aside); err != nil {
			restore(moved)
			return fmt.Errorf("move old %s plugin aside: %w", tool.Name, err)
		}

		moved = append(moved, [2]string{aside, p.Dir})
	}

	if err := os.Rename(staged, target); err != nil {
		restore(moved)
		return fmt.Errorf("install %s plugin: %w", tool.Name, err)
	}

	return nil
}

func restore(moved [][2]string) {
	for _, m := range moved {
		_ = os.Rename(m[0], m[1])
	}
}
