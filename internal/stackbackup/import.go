package stackbackup

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ImportOptions holds options for `obol stack import`.
type ImportOptions struct {
	Input       string
	Force       bool
	SkipCluster bool
	ClusterOnly bool
	SkipSync    bool
}

// Import restores an export archive. Host state (config + data) is restored
// first so a subsequent `obol stack up` mounts the right brains and
// keystores; if the cluster is already reachable the etcd-resident resources
// are re-applied and agent instances re-synced. When the cluster is down,
// Import restores host state and prints the two remaining steps instead of
// failing — re-run with --cluster-only after `obol stack up`.
func Import(cfg *config.Config, opts ImportOptions, u *ui.UI) error {
	manifest, err := readArchiveManifest(opts.Input)
	if err != nil {
		return err
	}

	u.Info("Importing stack backup")
	u.Detail("Created", manifest.CreatedAt)
	u.Detail("Obol version", manifest.ObolVersion)
	if manifest.StackID != "" {
		u.Detail("Stack ID", manifest.StackID)
	}
	if manifest.ConfigDir != cfg.ConfigDir || manifest.DataDir != cfg.DataDir {
		u.Warn("Archive was taken from different config/data paths — helmfiles and k3d.yaml embed absolute paths.")
		u.Detail("Archive config", manifest.ConfigDir)
		u.Detail("This host", cfg.ConfigDir)
		u.Warn("Cross-machine restore may need 'obol stack init --force' to re-render paths (this regenerates the stack ID).")
	}

	scratch, err := os.MkdirTemp("", "obol-stack-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	if !opts.ClusterOnly {
		if existing := readStackID(cfg.ConfigDir); existing != "" && !opts.Force {
			return fmt.Errorf("config dir %s already holds stack %q — purge it first or pass --force to overwrite", cfg.ConfigDir, existing)
		}
		if err := extractHostState(cfg, opts.Input, scratch, u); err != nil {
			return err
		}
		u.Success("Host state restored (config + agent data)")
		if w := manifest.component("wallets"); w != nil && w.Included {
			u.Detail("Wallets", "restored inside agent data dirs; portable copies remain in the archive under wallets/")
		}
	} else if err := extractScratchOnly(opts.Input, scratch); err != nil {
		return err
	}

	if opts.SkipCluster {
		u.Info("Skipping cluster restore (--skip-cluster)")
		return nil
	}

	clusterDir := filepath.Join(scratch, "cluster")
	if _, err := os.Stat(clusterDir); err != nil {
		u.Info("Archive has no cluster component — nothing to apply in-cluster.")
		printNextSteps(u, opts.Input, false)
		return nil
	}

	if err := kubectl.EnsureCluster(cfg); err != nil {
		printNextSteps(u, opts.Input, true)
		return nil
	}

	u.Info("Re-applying cluster resources...")
	applyCluster(cfg, clusterDir, u)

	if !opts.SkipSync {
		syncAgents(cfg, u)
	}

	u.Blank()
	u.Success("Import complete")
	u.Print("  Paid-inference purchases are not restored (pre-signed auths expire) — re-run buy flows as needed.")
	u.Print("  ERC-8004 registrations are bound to the tunnel hostname — re-run 'obol sell register' if it changed.")
	return nil
}

// extractHostState streams the archive once, routing config/ entries into
// ConfigDir, data/ entries into DataDir, and cluster/ entries into scratch.
func extractHostState(cfg *config.Config, input, scratch string, u *ui.UI) error {
	return walkArchive(input, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		switch {
		case clean == ManifestFileName:
			return nil
		case strings.HasPrefix(clean, "config"+string(os.PathSeparator)):
			return extractEntry(tr, hdr, cfg.ConfigDir, strings.TrimPrefix(clean, "config"+string(os.PathSeparator)))
		case strings.HasPrefix(clean, "data"+string(os.PathSeparator)):
			return extractEntry(tr, hdr, cfg.DataDir, strings.TrimPrefix(clean, "data"+string(os.PathSeparator)))
		default:
			return extractEntry(tr, hdr, scratch, clean)
		}
	})
}

// extractScratchOnly pulls just cluster/ + wallets/ into scratch
// (--cluster-only re-runs after `obol stack up`).
func extractScratchOnly(input, scratch string) error {
	return walkArchive(input, func(tr *tar.Reader, hdr *tar.Header, clean string) error {
		if strings.HasPrefix(clean, "config"+string(os.PathSeparator)) || strings.HasPrefix(clean, "data"+string(os.PathSeparator)) {
			return nil
		}
		return extractEntry(tr, hdr, scratch, clean)
	})
}

func walkArchive(input string, fn func(*tar.Reader, *tar.Header, string) error) error {
	f, err := os.Open(input)
	if err != nil {
		return err
	}
	defer f.Close()
	counted := &countingReader{r: f}
	gz, err := gzip.NewReader(counted)
	if err != nil {
		return fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(&ratioGuard{r: gz, compressed: &counted.n})
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean, err := sanitizeEntryName(hdr.Name)
		if err != nil {
			return err
		}
		if err := fn(tr, hdr, clean); err != nil {
			return fmt.Errorf("extract %s: %w", hdr.Name, err)
		}
	}
}

// syncAgents re-deploys every restored hermes/openclaw instance from its
// on-disk helmfile so deployments, remote-signer Secrets, and tokens line up
// with the restored state. Best-effort per instance.
func syncAgents(cfg *config.Config, u *ui.UI) {
	for _, id := range listInstances(cfg, "hermes") {
		u.Infof("Syncing hermes instance %s...", id)
		if err := hermes.Sync(cfg, id, u); err != nil {
			u.Warnf("hermes sync %s failed (run 'obol agent sync %s' manually): %v", id, id, err)
		}
	}
	for _, id := range listInstances(cfg, "openclaw") {
		u.Infof("Syncing openclaw instance %s...", id)
		if err := openclaw.Sync(cfg, id, u); err != nil {
			u.Warnf("openclaw sync %s failed (run 'obol openclaw sync %s' manually): %v", id, id, err)
		}
	}
}

func listInstances(cfg *config.Config, runtime string) []string {
	entries, err := os.ReadDir(filepath.Join(cfg.ConfigDir, "applications", runtime))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

func printNextSteps(u *ui.UI, input string, clusterComponentPresent bool) {
	u.Blank()
	u.Bold("Next steps:")
	u.Print("  1. obol stack up")
	if clusterComponentPresent {
		u.Printf("  2. obol stack import %s --cluster-only", input)
	}
}
