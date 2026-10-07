package stackbackup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	// StackUp brings the restored stack's cluster up with the full
	// `obol stack up` path (state replay included). Injected by the CLI —
	// internal/stack imports this package. When nil, Import prints the
	// remaining steps instead.
	StackUp func() error
}

// Import restores an export archive and, unless --skip-cluster, finishes
// the job instead of printing homework:
//
//  1. host state (config + agent data) is restored, and absolute paths baked
//     into the archive's config (helmfiles, k3d.yaml) are rewritten to this
//     host's config/data dirs;
//  2. a cluster left over from a `stack init`/`stack up` that this --force
//     import replaced is removed when it holds no agents or offers (it was
//     created moments ago and would otherwise squat on the ingress ports);
//  3. the restored stack is brought up via StackUp when its cluster isn't
//     running;
//  4. etcd-resident resources are applied and agent instances re-synced.
//
// It never deletes a cluster that holds agents/offers, and never applies an
// archive into another stack's cluster.
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

	scratch, err := os.MkdirTemp("", "obol-stack-import-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)

	// The stack this import replaces (fresh `stack init`, possibly `up`).
	replacedStackID := readStackID(cfg.ConfigDir)

	if !opts.ClusterOnly {
		if replacedStackID != "" && !opts.Force {
			return fmt.Errorf("config dir %s already holds stack %q — purge it first or pass --force to overwrite", cfg.ConfigDir, replacedStackID)
		}
		if err := extractHostState(cfg, opts.Input, scratch, u); err != nil {
			return err
		}
		if n, err := rewriteRestoredPaths(cfg, manifest); err != nil {
			return fmt.Errorf("rewrite restored config paths: %w", err)
		} else if n > 0 {
			u.Infof("Re-pointed %d config file(s) from %s to this host's paths", n, manifest.ConfigDir)
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
	_, statErr := os.Stat(clusterDir)
	hasClusterComponent := statErr == nil

	if err := ensureImportCluster(cfg, opts, replacedStackID, u); err != nil {
		if errors.Is(err, errNoStackUp) {
			printNextSteps(u, opts.Input, hasClusterComponent)
			return nil
		}
		return err
	}

	if !hasClusterComponent {
		u.Info("Archive has no cluster component — nothing to apply in-cluster.")
	} else {
		u.Info("Re-applying cluster resources...")
		applyCluster(cfg, clusterDir, u)
		if !opts.SkipSync {
			syncAgents(cfg, u)
		}
	}

	u.Blank()
	u.Success("Import complete")
	u.Print("  Paid-inference purchases are not restored (pre-signed auths expire) — re-run buy flows as needed.")
	u.Print("  ERC-8004 registrations are bound to the tunnel hostname — re-run 'obol sell register' if it changed.")
	return nil
}

// ensureClusterFn reports whether the kubeconfig reaches an API server.
// Swappable in tests.
var ensureClusterFn = kubectl.EnsureCluster

// errNoStackUp means the cluster isn't running and no StackUp hook was given.
var errNoStackUp = errors.New("cluster not running")

// ensureImportCluster leaves the kubeconfig reaching THIS stack's running
// cluster, fixing what it safely can on the way.
func ensureImportCluster(cfg *config.Config, opts ImportOptions, replacedStackID string, u *ui.UI) error {
	if ensureClusterFn(cfg) == nil {
		err := verifyClusterIdentity(cfg)
		if err == nil {
			return nil
		}
		var mismatch *clusterMismatchError
		if !errors.As(err, &mismatch) || mismatch.OtherCluster == "" {
			return fmt.Errorf("refusing to apply cluster resources: %w", err)
		}
		if !opts.ClusterOnly && replacedStackID != "" && mismatch.OtherCluster == "obol-stack-"+replacedStackID {
			// The cluster of the stack config this --force import just
			// overwrote: its config (and keystore passwords) are already gone.
			if populated, why := clusterHoldsWorkloads(cfg); populated {
				return fmt.Errorf("the pre-import cluster %s still holds %s; export it or delete it yourself ('k3d cluster delete %s'), then run 'obol stack import %s --cluster-only'",
					mismatch.OtherCluster, why, mismatch.OtherCluster, opts.Input)
			}
			u.Infof("Removing the pre-import cluster %s (created by the stack this import replaced; holds no agents or offers) so the restored stack can claim the ingress ports", mismatch.OtherCluster)
			if err := deleteK3dClusterFn(cfg, mismatch.OtherCluster); err != nil {
				return fmt.Errorf("remove pre-import cluster %s: %w", mismatch.OtherCluster, err)
			}
		} else {
			// Some other stack's cluster behind a stale kubeconfig: leave it
			// alone; `stack up` writes a fresh kubeconfig for ours.
			u.Infof("Kubeconfig reaches another stack's cluster (%s); leaving it untouched and bringing up stack %q", mismatch.OtherCluster, mismatch.StackID)
		}
	}

	if opts.StackUp == nil {
		return errNoStackUp
	}
	u.Infof("Bringing up the restored stack (obol stack up)...")
	if err := opts.StackUp(); err != nil {
		return fmt.Errorf("obol stack up: %w (host state is restored; fix the error and re-run 'obol stack import %s --cluster-only')", err, opts.Input)
	}
	if err := ensureClusterFn(cfg); err != nil {
		return fmt.Errorf("cluster still unreachable after stack up: %w", err)
	}
	if err := verifyClusterIdentity(cfg); err != nil {
		return fmt.Errorf("refusing to apply cluster resources: %w", err)
	}
	return nil
}

// clusterHoldsWorkloads reports whether the reachable cluster has Agent CRs
// or ServiceOffers (sub-agent wallets / live offers that a delete would
// destroy). Errors count as populated — never delete on uncertainty.
var clusterHoldsWorkloads = func(cfg *config.Config) (bool, string) {
	bin, kubeconfig := kubectl.Paths(cfg)
	for _, kind := range []string{"agents.obol.org", "serviceoffers.obol.org"} {
		out, err := kubectl.Output(bin, kubeconfig, "get", kind, "-A", "-o", "name")
		if err != nil {
			return true, "resources that could not be listed (" + kind + ")"
		}
		if strings.TrimSpace(out) != "" {
			return true, kind
		}
	}
	return false, ""
}

var deleteK3dClusterFn = func(cfg *config.Config, name string) error {
	out, err := exec.Command(cfg.ToolPath("k3d"), "cluster", "delete", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// rewriteRestoredPaths re-points absolute config/data paths baked into the
// restored config tree (helmfiles, k3d.yaml, values files) at this host's
// dirs. Archives from another machine, user, or dev/prod mode otherwise
// mount nonexistent host paths. Binary and large files are skipped.
func rewriteRestoredPaths(cfg *config.Config, m *Manifest) (int, error) {
	type pair struct{ from, to string }
	var pairs []pair
	for _, p := range []pair{{m.ConfigDir, cfg.ConfigDir}, {m.DataDir, cfg.DataDir}} {
		if p.from != "" && p.from != p.to {
			pairs = append(pairs, p)
		}
	}
	if len(pairs) == 0 {
		return 0, nil
	}
	// Longest source first so a nested path is never half-rewritten.
	if len(pairs) == 2 && len(pairs[1].from) > len(pairs[0].from) {
		pairs[0], pairs[1] = pairs[1], pairs[0]
	}
	changed := 0
	err := filepath.WalkDir(cfg.ConfigDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		out := string(data)
		for _, p := range pairs {
			out = strings.ReplaceAll(out, p.from, p.to)
		}
		if out == string(data) {
			return nil
		}
		changed++
		return os.WriteFile(path, []byte(out), info.Mode().Perm())
	})
	return changed, err
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
