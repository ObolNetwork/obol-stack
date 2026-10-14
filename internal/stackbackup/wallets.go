package stackbackup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/fsutil"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// The wallets/ component holds one walletbackup-format file per host-side
// wallet, named after the agent namespace: wallets/<namespace>.json (plain)
// or wallets/<namespace>.enc (passphrase-encrypted). It is the portable copy
// of each keystore + password, and the only copy when the exporting user
// cannot read the keystore dir on the host (on Linux the remote-signer pod
// owns it as UID 65532, mode 0700). Import re-materialises a keystore from
// it whenever the data/ copy did not bring the file back.

const (
	walletsDir         = "wallets"
	walletExtPlain     = ".json"
	walletExtEncrypted = ".enc"
)

// legacyOpenClawRuntime is the config dir name of OpenClaw instances left
// over from <= v0.15 (removed in v0.16; their wallets still ride along).
const legacyOpenClawRuntime = "openclaw"

// buildHostWalletBackupFn is swappable in tests.
var buildHostWalletBackupFn = hermes.BuildHostWalletBackup

// hostWallets lists every host-side wallet: Hermes instances and leftover
// OpenClaw instances whose deployment dir holds wallet.json. Sub-agent
// wallets (agent-*) live only in cluster Secrets and are covered by the
// cluster component instead.
func hostWallets(cfg *config.Config) []hermes.HostWallet {
	var out []hermes.HostWallet
	for _, id := range hermes.FindInstancesWithWallets(cfg) {
		out = append(out, hermes.HermesHostWallet(cfg, id))
	}
	legacyRoot := filepath.Join(cfg.ConfigDir, "applications", legacyOpenClawRuntime)
	entries, _ := os.ReadDir(legacyRoot)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		deployDir := filepath.Join(legacyRoot, e.Name())
		if _, err := hermes.ReadWalletMetadata(deployDir); err != nil {
			continue
		}
		ns := legacyOpenClawRuntime + "-" + e.Name()
		out = append(out, hermes.HostWallet{
			Namespace:   ns,
			Instance:    e.Name(),
			DeployDir:   deployDir,
			KeystoreDir: filepath.Join(cfg.DataDir, ns, "remote-signer-keystores"),
		})
	}
	return out
}

// walletExport is the outcome of capturing one host wallet.
type walletExport struct {
	wallet  hermes.HostWallet
	address string
	file    string // archive path, e.g. wallets/hermes-obol-agent.enc
	route   hermes.KeystoreRoute
	err     error
}

// note is the manifest line for this wallet (addresses only, no secrets).
func (w walletExport) note() string {
	if w.err != nil {
		return fmt.Sprintf("%s: FAILED (%v)", w.wallet.Namespace, w.err)
	}
	how := "keystore also in data/"
	if w.route == hermes.KeystoreRouteK3dNode {
		how = "only copy: host keystore dir not readable by the exporting user, read through the k3d node"
	}
	return fmt.Sprintf("%s: %s %s (%s)", w.wallet.Namespace, w.address, w.file, how)
}

// summary is the human line printed at the end of an export.
func (w walletExport) summary() string {
	if w.route == hermes.KeystoreRouteK3dNode {
		return fmt.Sprintf("%s -> %s only (the host keystore dir is owned by the remote-signer pod and not readable by you; read through the k3d node)", w.address, w.file)
	}
	return fmt.Sprintf("%s -> %s (keystore also under data/%s/)", w.address, w.file, w.wallet.Namespace)
}

// exportWallets writes a walletbackup-format file for every host wallet into
// destDir. A wallet that cannot be captured is reported loudly here and in
// the export summary: the archive is then NOT a complete backup of it.
func exportWallets(cfg *config.Config, destDir string, wallets []hermes.HostWallet, passphrase string, u *ui.UI) []walletExport {
	if len(wallets) == 0 {
		return nil
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		u.Warnf("Could not create wallet staging dir: %v", err)
		return nil
	}
	ext := walletExtPlain
	if passphrase != "" {
		ext = walletExtEncrypted
	}
	out := make([]walletExport, 0, len(wallets))
	for _, w := range wallets {
		res := walletExport{wallet: w, file: walletsDir + "/" + w.Namespace + ext}
		res.err = func() error {
			backup, route, err := buildHostWalletBackupFn(cfg, w)
			if err != nil {
				return err
			}
			res.route = route
			res.address = backup.Wallets[0].Address
			payload, _, err := walletbackup.Encode(backup, passphrase)
			if err != nil {
				return err
			}
			return fsutil.WriteSecret(filepath.Join(destDir, w.Namespace+ext), payload)
		}()
		if res.err != nil {
			u.Warnf("Wallet of %s NOT captured: %v", w.Namespace, res.err)
			if errors.Is(res.err, fs.ErrPermission) {
				u.Printf("  Its keystore dir (%s) is owned by the remote-signer pod. Start the stack ('obol stack up') so export can read it through the k3d node, then export again.", w.KeystoreDir)
			}
		}
		out = append(out, res)
	}
	return out
}

func walletNotes(results []walletExport) []string {
	notes := make([]string, 0, len(results))
	for _, r := range results {
		notes = append(notes, r.note())
	}
	return notes
}

// coveredByWalletBackup reports whether path lies inside the keystore dir
// of a wallet that the wallets/ component captured, i.e. a skipped data/
// entry there is not a lost key.
func coveredByWalletBackup(path string, results []walletExport) bool {
	for _, r := range results {
		if r.err != nil {
			continue
		}
		dir := filepath.Clean(r.wallet.KeystoreDir)
		if p := filepath.Clean(path); p == dir || strings.HasPrefix(p, dir+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// walletRestore is the outcome of restoring one wallet's keystore on import.
type walletRestore struct {
	namespace string
	address   string
	source    string // archive file the keystore came from
	path      string // keystore file on this host
	written   bool   // re-materialised from wallets/ by this import
	note      string // set when the keystore was left as found
	err       error
}

// restartRemoteSignerFn restarts a namespace's remote-signer so it loads a
// keystore written under a running pod. Swappable in tests.
var restartRemoteSignerFn = func(cfg *config.Config, namespace string) error {
	bin, kubeconfig := kubectl.Paths(cfg)
	return kubectl.RunSilent(bin, kubeconfig, "rollout", "restart", "deployment/remote-signer", "-n", namespace)
}

// restoreWalletKeystores makes sure every wallet in the archive's wallets/
// component has its keystore file in its namespace's keystore dir. A keystore
// the data/ component already brought back is left as is; a missing one
// (export could not read the host copy, or the archive lost it) is
// re-materialised from the backup after checking that it decrypts to the
// recorded address. Files are written 0600 in a 0700 dir owned by the
// importing user; the remote-signer pod's root fix-permissions init
// container hands them to the signer's user when it starts.
func restoreWalletKeystores(cfg *config.Config, dir string, opts ImportOptions, u *ui.UI) []walletRestore {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	passphrase, havePass := opts.Passphrase, opts.HasPassFlag
	resolvePass := func() (string, error) {
		if havePass {
			return passphrase, nil
		}
		if !u.CanPrompt() {
			return "", errors.New("wallet backup is encrypted: pass --passphrase (the one given to 'obol stack export')")
		}
		p, err := u.SecretInput("Wallet backup passphrase (from 'obol stack export')")
		if err != nil {
			return "", fmt.Errorf("read passphrase: %w", err)
		}
		passphrase, havePass = p, true
		return p, nil
	}

	var out []walletRestore
	for _, e := range entries {
		name := e.Name()
		ext := filepath.Ext(name)
		ns := strings.TrimSuffix(name, ext)
		if !e.Type().IsRegular() || (ext != walletExtPlain && ext != walletExtEncrypted) || !isWalletNamespace(ns) {
			continue
		}
		res := restoreOneWallet(cfg, filepath.Join(dir, name), ns, walletsDir+"/"+name, resolvePass)
		out = append(out, res)
	}
	return out
}

func restoreOneWallet(cfg *config.Config, file, ns, source string, resolvePass func() (string, error)) walletRestore {
	res := walletRestore{namespace: ns, source: source}
	runtime, id, _ := strings.Cut(ns, "-")
	deployDir := filepath.Join(cfg.ConfigDir, "applications", runtime, id)
	keystoreDir := filepath.Join(cfg.DataDir, ns, "remote-signer-keystores")

	// Fast path: the restored config names the keystore and data/ brought
	// it back — nothing to decrypt.
	meta, metaErr := hermes.ReadWalletMetadata(deployDir)
	if metaErr == nil && meta.KeystoreUUID != "" {
		res.address = meta.Address
		res.path = filepath.Join(keystoreDir, meta.KeystoreUUID+".json")
		if present, note := keystorePresent(res.path); present || note != "" {
			res.note = note
			return res
		}
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		res.err = err
		return res
	}
	pass := ""
	if walletbackup.IsEncrypted(raw) {
		if pass, err = resolvePass(); err != nil {
			res.err = err
			return res
		}
	}
	backup, err := walletbackup.Decode(raw, pass)
	if err != nil {
		res.err = err
		return res
	}
	w := backup.Wallets[0]
	res.address = w.Address
	if w.KeystoreUUID == "" || w.KeystoreUUID != filepath.Base(w.KeystoreUUID) || strings.HasPrefix(w.KeystoreUUID, ".") {
		res.err = fmt.Errorf("backup has an invalid keystore id %q", w.KeystoreUUID)
		return res
	}
	if metaErr == nil && (meta.KeystoreUUID != w.KeystoreUUID || !strings.EqualFold(meta.Address, w.Address)) {
		res.err = fmt.Errorf("backup holds %s but the restored config expects %s — left untouched", w.Address, meta.Address)
		return res
	}
	res.path = filepath.Join(keystoreDir, w.KeystoreUUID+".json")
	if present, note := keystorePresent(res.path); present || note != "" {
		res.note = note
		return res
	}
	if err := w.VerifyKeystore(); err != nil {
		res.err = err
		return res
	}
	if err := os.MkdirAll(keystoreDir, 0o700); err != nil {
		res.err = err
		return res
	}
	if err := fsutil.WriteSecret(res.path, w.Keystore); err != nil {
		res.err = err
		return res
	}
	res.written = true
	return res
}

// keystorePresent reports whether a non-empty keystore file exists at path.
// A path this user cannot inspect (keystore dir owned by the remote-signer
// pod) is reported via note and left alone.
func keystorePresent(path string) (bool, string) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		return info.Mode().IsRegular() && info.Size() > 0, ""
	case errors.Is(err, fs.ErrPermission):
		return false, "keystore dir not readable by you (owned by the remote-signer pod); left as found"
	default:
		return false, ""
	}
}

// isWalletNamespace accepts wallets/ file stems that name a host wallet's
// agent namespace (hermes-<id>, openclaw-<id>) and nothing path-like.
func isWalletNamespace(ns string) bool {
	if ns == "" || ns != filepath.Base(ns) || strings.HasPrefix(ns, ".") {
		return false
	}
	for _, p := range []string{agentruntime.Namespace(agentruntime.Hermes, ""), legacyOpenClawRuntime + "-"} {
		if strings.HasPrefix(ns, p) && len(ns) > len(p) {
			return true
		}
	}
	return false
}

// printWalletRestores prints exactly what happened to each wallet.
func printWalletRestores(results []walletRestore, u *ui.UI) {
	if len(results) == 0 {
		return
	}
	written := false
	for _, r := range results {
		label := "Wallet " + r.namespace
		switch {
		case r.err != nil:
			u.Warnf("%s: keystore NOT restored: %v", label, r.err)
			u.Printf("  %s", manualRestoreHint(r))
		case r.written:
			written = true
			u.Detail(label, fmt.Sprintf("%s keystore restored from %s to %s (decrypts to this address)", r.address, r.source, r.path))
		case r.note != "":
			u.Detail(label, fmt.Sprintf("%s %s (%s)", r.address, r.note, filepath.Dir(r.path)))
		default:
			u.Detail(label, fmt.Sprintf("%s keystore restored from the archive's data/ copy (%s)", r.address, r.path))
		}
	}
	if written {
		u.Print("  Restored keystores are yours (0600, dir 0700); the remote-signer pod's fix-permissions init container hands them to the signer user when it starts.")
	}
}

// manualRestoreHint tells the operator how to restore a wallet by hand from
// the archive's portable copy.
func manualRestoreHint(r walletRestore) string {
	extract := "tar -xzf <archive> " + r.source
	if id, ok := strings.CutPrefix(r.namespace, agentruntime.Namespace(agentruntime.Hermes, "")); ok {
		return fmt.Sprintf("Restore it by hand: %s && obol agent wallet restore %s --input %s --force", extract, id, r.source)
	}
	return fmt.Sprintf("Its keystore and password are in %s (%s); 'obol agent wallet restore <hermes-instance> --input %s' moves it into a Hermes agent.", r.source, extract, r.source)
}

func hasWrittenWallet(results []walletRestore) bool {
	for _, r := range results {
		if r.written {
			return true
		}
	}
	return false
}

// restartSignersForRestoredKeystores restarts the remote-signer of every
// namespace whose keystore this import wrote while the cluster was already
// running, so the signer loads it. Best-effort.
func restartSignersForRestoredKeystores(cfg *config.Config, results []walletRestore, u *ui.UI) {
	for _, r := range results {
		if !r.written {
			continue
		}
		if err := restartRemoteSignerFn(cfg, r.namespace); err != nil {
			if strings.Contains(err.Error(), "NotFound") || strings.Contains(err.Error(), "not found") {
				continue // no signer yet: the one sync creates loads the keystore on start
			}
			u.Warnf("Could not restart the remote-signer in %s to load its restored keystore: %v", r.namespace, err)
			u.Printf("  Run 'obol kubectl -n %s rollout restart deployment/remote-signer' once the namespace is up.", r.namespace)
			continue
		}
		u.Detail("Restarted", "remote-signer in "+r.namespace+" (loads the restored keystore)")
	}
}
