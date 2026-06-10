package stackbackup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/openclaw"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/version"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// ExportOptions holds options for `obol stack export`.
type ExportOptions struct {
	Output      string
	Passphrase  string
	HasPassFlag bool
}

// Export captures the stack into a tar.gz archive and returns its path.
// Safe with the cluster up (agent deployments are quiesced while their data
// dirs are copied — never copy a live SQLite state.db) and with the cluster
// down (host dirs are the source of truth; the cluster component is skipped).
func Export(cfg *config.Config, opts ExportOptions, u *ui.UI) (string, error) {
	if _, err := os.Stat(cfg.ConfigDir); err != nil {
		return "", fmt.Errorf("no stack config at %s — nothing to export", cfg.ConfigDir)
	}

	stackID := readStackID(cfg.ConfigDir)
	manifest := &Manifest{
		Version:     ManifestVersion,
		ObolVersion: version.Full(),
		StackID:     stackID,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ConfigDir:   cfg.ConfigDir,
		DataDir:     cfg.DataDir,
	}

	// Resolve the wallet passphrase up front so the interactive prompt does
	// not interrupt long-running copy work.
	hermesWallets := hermes.FindInstancesWithWallets(cfg)
	openclawWallets := openclaw.FindInstancesWithWallets(cfg)
	passphrase := opts.Passphrase
	if len(hermesWallets)+len(openclawWallets) > 0 && !opts.HasPassFlag {
		var err error
		passphrase, err = walletbackup.PromptPassphrase(opts.Passphrase, opts.HasPassFlag, u)
		if err != nil {
			return "", err
		}
	}

	staging, err := os.MkdirTemp("", "obol-stack-export-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)

	clusterUp := kubectl.EnsureCluster(cfg) == nil
	dataNamespaces := selectDataNamespaces(cfg.DataDir)

	// Quiesce agent workloads so data dirs (SQLite state.db and friends) are
	// copied without live writers. Restored before returning, even on error.
	if clusterUp && len(dataNamespaces) > 0 {
		u.Info("Pausing agent workloads for a consistent snapshot...")
		quiesced := quiesceNamespaces(cfg, dataNamespaces, u)
		defer restoreQuiesced(cfg, quiesced, u)
	}

	// Component: wallets (portable, individually encryptable).
	walletNotes := exportWallets(cfg, filepath.Join(staging, "wallets"), hermesWallets, openclawWallets, passphrase, u)
	manifest.Components = append(manifest.Components, Component{
		Name:     "wallets",
		Included: len(walletNotes) > 0,
		Notes:    walletNotes,
	})

	// Component: cluster resources (etcd drift).
	clusterComponent := Component{Name: "cluster"}
	if clusterUp {
		notes, err := harvestCluster(cfg, filepath.Join(staging, "cluster"))
		if err != nil {
			return "", fmt.Errorf("harvest cluster resources: %w", err)
		}
		clusterComponent.Included = true
		clusterComponent.Notes = notes
	} else {
		clusterComponent.Notes = []string{"cluster not running — etcd-resident resources not captured"}
	}
	manifest.Components = append(manifest.Components, clusterComponent)

	manifest.Components = append(manifest.Components,
		Component{Name: "config", Included: true, Notes: []string{"excludes kubeconfig*, defaults/"}},
		Component{Name: "data", Included: len(dataNamespaces) > 0, Notes: dataNamespaces},
	)

	// Assemble the archive: manifest first, then staged components, then
	// config and data trees streamed straight from their source roots.
	outputPath := opts.Output
	if outputPath == "" {
		id := stackID
		if id == "" {
			id = "stack"
		}
		outputPath = fmt.Sprintf("obol-stack-backup-%s-%s.tar.gz", id, time.Now().UTC().Format("20060102-150405"))
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}

	w, err := newArchiveWriter(outputPath)
	if err != nil {
		return "", err
	}
	archiveErr := func() error {
		if err := w.addBytes(ManifestFileName, manifestJSON, 0o600); err != nil {
			return err
		}
		for _, sub := range []string{"wallets", "cluster"} {
			dir := filepath.Join(staging, sub)
			if _, err := os.Stat(dir); err != nil {
				continue
			}
			if warns, err := w.addTree(dir, sub, nil); err != nil {
				return err
			} else {
				warnAll(u, warns)
			}
		}
		warns, err := w.addTree(cfg.ConfigDir, "config", skipConfigEntry)
		if err != nil {
			return err
		}
		warnAll(u, warns)
		for _, ns := range dataNamespaces {
			warns, err := w.addTree(filepath.Join(cfg.DataDir, ns), "data/"+ns, nil)
			if err != nil {
				return err
			}
			warnAll(u, warns)
		}
		return nil
	}()
	if closeErr := w.Close(); archiveErr == nil {
		archiveErr = closeErr
	}
	if archiveErr != nil {
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("write archive: %w", archiveErr)
	}

	info, _ := os.Stat(outputPath)
	u.Blank()
	u.Success("Stack export created")
	u.Detail("Output", outputPath)
	if info != nil {
		u.Detail("Size", fmt.Sprintf("%.1f MB", float64(info.Size())/(1024*1024)))
	}
	for _, c := range manifest.Components {
		status := "included"
		if !c.Included {
			status = "skipped"
		}
		u.Detail(c.Name, status)
	}
	if passphrase == "" && len(walletNotes) > 0 {
		u.Warn("Wallet backups inside the archive are UNENCRYPTED")
	}
	u.Warn("Archive contains keystore passwords and provider API keys — store securely")
	return outputPath, nil
}

func warnAll(u *ui.UI, warns []string) {
	for _, w := range warns {
		u.Warnf("%s", w)
	}
}

// exportWallets writes walletbackup-format files for every instance with a
// wallet. Failures are recorded as notes, not fatal: the same keys also ride
// along inside the data/ component's keystore dirs.
func exportWallets(cfg *config.Config, destDir string, hermesIDs, openclawIDs []string, passphrase string, u *ui.UI) []string {
	if len(hermesIDs)+len(openclawIDs) == 0 {
		return nil
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		u.Warnf("Could not create wallet staging dir: %v", err)
		return nil
	}
	ext := "json"
	if passphrase != "" {
		ext = "enc"
	}
	var notes []string
	for _, id := range hermesIDs {
		out := filepath.Join(destDir, fmt.Sprintf("hermes-%s.%s", id, ext))
		err := hermes.BackupWalletCmd(cfg, id, hermes.BackupWalletOptions{
			Output: out, Passphrase: passphrase, HasPassFlag: true,
		}, u)
		notes = append(notes, walletNote("hermes", id, err))
	}
	for _, id := range openclawIDs {
		out := filepath.Join(destDir, fmt.Sprintf("openclaw-%s.%s", id, ext))
		err := openclaw.BackupWalletCmd(cfg, id, openclaw.BackupWalletOptions{
			Output: out, Passphrase: passphrase, HasPassFlag: true,
		}, u)
		notes = append(notes, walletNote("openclaw", id, err))
	}
	return notes
}

func walletNote(runtime, id string, err error) string {
	if err != nil {
		return fmt.Sprintf("%s/%s: FAILED (%v)", runtime, id, err)
	}
	return fmt.Sprintf("%s/%s", runtime, id)
}

// quiescedDeploy remembers a deployment's replica count so it can be
// restored after the data copy.
type quiescedDeploy struct {
	namespace string
	name      string
	replicas  string
}

// quiesceNamespaces scales every deployment in the given namespaces to zero
// and waits for their pods to terminate. Best-effort: a namespace that fails
// to quiesce is reported and its data copied anyway (better a possibly-torn
// copy than none).
func quiesceNamespaces(cfg *config.Config, namespaces []string, u *ui.UI) []quiescedDeploy {
	bin, kubeconfig := kubectl.Paths(cfg)
	var quiesced []quiescedDeploy
	for _, ns := range namespaces {
		out, err := kubectl.Output(bin, kubeconfig, "get", "deploy", "-n", ns,
			"-o", "jsonpath={range .items[*]}{.metadata.name} {.spec.replicas}{\"\\n\"}{end}")
		if err != nil {
			continue // namespace may not exist in the cluster
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 || fields[1] == "0" {
				continue
			}
			if err := kubectl.RunSilent(bin, kubeconfig, "scale", "deploy", fields[0], "-n", ns, "--replicas=0"); err != nil {
				u.Warnf("Could not pause %s/%s — its data copy may be inconsistent: %v", ns, fields[0], err)
				continue
			}
			quiesced = append(quiesced, quiescedDeploy{namespace: ns, name: fields[0], replicas: fields[1]})
		}
	}
	if len(quiesced) > 0 {
		waitForScaledDown(cfg, quiesced, u)
	}
	return quiesced
}

// waitForScaledDown polls the scaled deployments' status.replicas until
// every one reports zero pods. Other workloads in the namespace (the
// remote-signer StatefulSet, jobs) are intentionally ignored — they do not
// write agent data, and they never terminate during a quiesce.
func waitForScaledDown(cfg *config.Config, quiesced []quiescedDeploy, u *ui.UI) {
	bin, kubeconfig := kubectl.Paths(cfg)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		remaining := 0
		for _, q := range quiesced {
			out, err := kubectl.Output(bin, kubeconfig, "get", "deploy", q.name, "-n", q.namespace,
				"-o", "jsonpath={.status.replicas}")
			if err != nil {
				continue // deployment gone counts as scaled down
			}
			if v := strings.TrimSpace(out); v != "" && v != "0" {
				remaining++
			}
		}
		if remaining == 0 {
			return
		}
		time.Sleep(3 * time.Second)
	}
	u.Warn("Timed out waiting for agent pods to stop — data copy may be inconsistent")
}

func restoreQuiesced(cfg *config.Config, quiesced []quiescedDeploy, u *ui.UI) {
	if len(quiesced) == 0 {
		return
	}
	bin, kubeconfig := kubectl.Paths(cfg)
	for _, q := range quiesced {
		if err := kubectl.RunSilent(bin, kubeconfig, "scale", "deploy", q.name, "-n", q.namespace, "--replicas="+q.replicas); err != nil {
			u.Warnf("Could not resume %s/%s (scale back to %s replicas manually): %v", q.namespace, q.name, q.replicas, err)
		}
	}
	u.Info("Agent workloads resumed")
}
