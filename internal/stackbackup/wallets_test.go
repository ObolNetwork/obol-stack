package stackbackup

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/hermes"
	"github.com/ObolNetwork/obol-stack/internal/keystore"
	"github.com/ObolNetwork/obol-stack/internal/ui"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
)

const (
	walletTestID   = agentruntime.DefaultInstanceID
	walletTestNS   = "hermes-" + walletTestID
	walletTestPass = "export-passphrase"
)

// walletStack is a host stack with one Hermes wallet built from a synthetic
// keystore (internal/keystore), as `obol agent new` leaves it.
type walletStack struct {
	cfg      *config.Config
	material *keystore.Material
	keystore string // host keystore file
}

func newWalletStack(t *testing.T) walletStack {
	t.Helper()
	cfg := importTestConfig(t)
	m, err := keystore.GenerateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	writeWalletConfig(t, cfg, m)
	ksDir := agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, walletTestID)
	if err := os.MkdirAll(ksDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ks := filepath.Join(ksDir, m.KeystoreUUID+".json")
	if err := os.WriteFile(ks, m.KeystoreJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cfg.DataDir, walletTestNS, "hermes-data", "state.txt"), "agent state")
	return walletStack{cfg: cfg, material: m, keystore: ks}
}

// writeWalletConfig writes the instance's wallet.json and keystore password
// file, as the restored config dir carries them.
func writeWalletConfig(t *testing.T, cfg *config.Config, m *keystore.Material) {
	t.Helper()
	deployDir := hermes.DeploymentPath(cfg, walletTestID)
	if err := os.MkdirAll(deployDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := hermes.WriteWalletMetadata(deployDir, &hermes.WalletInfo{
		Address:      m.Address,
		PublicKey:    m.PublicKey,
		KeystoreUUID: m.KeystoreUUID,
		CreatedAt:    "2026-10-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := walletbackup.WriteValuesRemoteSigner(deployDir, "keystorePassword:\n  value: \""+m.Password+"\"\n"); err != nil {
		t.Fatal(err)
	}
}

// lockKeystoreDir makes the keystore dir unreadable, as the remote-signer
// pod (UID 65532, mode 0700) leaves it for the operator on a Linux host.
func lockKeystoreDir(t *testing.T, s walletStack) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root: a mode-000 dir is still readable")
	}
	dir := filepath.Dir(s.keystore)
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := os.ReadFile(s.keystore); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("keystore still readable after chmod 000: %v", err)
	}
}

// stubNodeRead stands in for the k3d-node read route: the real builder reads
// the host copy and fails with EACCES, and the node returns the keystore
// bytes, as `docker exec <server-0> cat` does on a running cluster.
func stubNodeRead(t *testing.T, s walletStack) *int {
	t.Helper()
	calls := 0
	orig := buildHostWalletBackupFn
	t.Cleanup(func() { buildHostWalletBackupFn = orig })
	buildHostWalletBackupFn = func(cfg *config.Config, w hermes.HostWallet) (*walletbackup.File, hermes.KeystoreRoute, error) {
		calls++
		if _, err := os.ReadFile(s.keystore); !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("host keystore read = %v, want permission denied", err)
		}
		return &walletbackup.File{
			Version:  walletbackup.Version,
			Instance: w.Instance,
			Wallets: []walletbackup.Wallet{{
				Address:          s.material.Address,
				PublicKey:        s.material.PublicKey,
				KeystoreUUID:     s.material.KeystoreUUID,
				Keystore:         s.material.KeystoreJSON,
				KeystorePassword: s.material.Password,
			}},
		}, hermes.KeystoreRouteK3dNode, nil
	}
	return &calls
}

func captureUI() (*ui.UI, *bytes.Buffer) {
	var buf bytes.Buffer
	return ui.NewForTest(&buf, &buf), &buf
}

func exportTo(t *testing.T, cfg *config.Config, passphrase string) (string, string) {
	t.Helper()
	u, out := captureUI()
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if _, err := Export(cfg, ExportOptions{Output: archive, Passphrase: passphrase, HasPassFlag: true}, u); err != nil {
		t.Fatalf("Export: %v\n%s", err, out)
	}
	return archive, out.String()
}

func archiveEntries(t *testing.T, archive string) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	if err := walkArchive(archive, func(_ *tar.Reader, _ *tar.Header, clean string) error {
		names[filepath.ToSlash(clean)] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return names
}

func walletsComponent(t *testing.T, archive string) Component {
	t.Helper()
	m, err := readArchiveManifest(archive)
	if err != nil {
		t.Fatal(err)
	}
	c := m.component("wallets")
	if c == nil {
		t.Fatal("manifest has no wallets component")
	}
	return *c
}

// derivedAddress decrypts a keystore in-process and returns only the
// address its private key derives.
func derivedAddress(t *testing.T, keystoreJSON []byte, password string) string {
	t.Helper()
	priv, err := keystore.DecryptV3(keystoreJSON, password)
	if err != nil {
		t.Fatalf("decrypt restored keystore: %v", err)
	}
	defer keystore.Zero(priv)
	key, err := ethcrypto.ToECDSA(priv)
	if err != nil {
		t.Fatal(err)
	}
	return ethcrypto.PubkeyToAddress(key.PublicKey).Hex()
}

func assertNoSecrets(t *testing.T, out string, m *keystore.Material) {
	t.Helper()
	if strings.Contains(out, m.Password) {
		t.Fatal("CLI output contains the keystore password")
	}
	var ks struct {
		Crypto struct {
			CipherText string `json:"ciphertext"`
		} `json:"crypto"`
	}
	if err := json.Unmarshal(m.KeystoreJSON, &ks); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ks.Crypto.CipherText) {
		t.Fatal("CLI output contains keystore ciphertext")
	}
}

func TestExport_UnreadableKeystoreDirCapturedViaWalletBackup(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	calls := stubNodeRead(t, s)

	archive, out := exportTo(t, s.cfg, walletTestPass)

	if *calls != 1 {
		t.Fatalf("wallet backup built %d times, want 1", *calls)
	}
	entries := archiveEntries(t, archive)
	if !entries["wallets/"+walletTestNS+".enc"] {
		t.Fatalf("archive lacks wallets/%s.enc: %v", walletTestNS, entries)
	}
	if !entries["data/"+walletTestNS+"/hermes-data/state.txt"] {
		t.Fatal("readable agent data missing from the archive")
	}
	if entries["data/"+walletTestNS+"/remote-signer-keystores/"+s.material.KeystoreUUID+".json"] {
		t.Fatal("unreadable keystore unexpectedly in data/")
	}
	// No generic "skipping <keystore dir>: permission denied" noise: the
	// wallet line says how the key was captured instead.
	if strings.Contains(out, "skipping") {
		t.Errorf("generic skip warning for a captured keystore dir:\n%s", out)
	}
	for _, want := range []string{"Wallet " + walletTestNS, s.material.Address, "wallets/" + walletTestNS + ".enc only", "remote-signer pod"} {
		if !strings.Contains(out, want) {
			t.Errorf("export output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "NOT") {
		t.Errorf("captured wallet reported as missing:\n%s", out)
	}
	assertNoSecrets(t, out, s.material)

	c := walletsComponent(t, archive)
	if !c.Included || len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "only copy") || !strings.Contains(c.Notes[0], s.material.Address) {
		t.Fatalf("wallets component = %+v", c)
	}
}

func TestExport_UncapturableWalletWarnsLoudly(t *testing.T) {
	s := newWalletStack(t)
	// Not k3d: there is no node to read the locked dir through.
	writeFile(t, filepath.Join(s.cfg.ConfigDir, ".stack-backend"), "k3s")
	lockKeystoreDir(t, s)

	archive, out := exportTo(t, s.cfg, "")

	for _, want := range []string{"Wallet of " + walletTestNS + " NOT captured", "permission denied", "obol stack up", "NOT in this archive: " + walletTestNS} {
		if !strings.Contains(out, want) {
			t.Errorf("export output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "UNENCRYPTED") {
		t.Errorf("no wallet was captured, yet the output warns about unencrypted wallet backups:\n%s", out)
	}
	if entries := archiveEntries(t, archive); entries["wallets/"+walletTestNS+".json"] {
		t.Fatal("failed wallet left a file under wallets/")
	}
	c := walletsComponent(t, archive)
	if c.Included || len(c.Notes) != 1 || !strings.Contains(c.Notes[0], "FAILED") {
		t.Fatalf("wallets component = %+v, want not included with a FAILED note", c)
	}
}

func TestExport_ReadableKeystoreGoesToDataAndWallets(t *testing.T) {
	s := newWalletStack(t)

	archive, out := exportTo(t, s.cfg, "")

	entries := archiveEntries(t, archive)
	for _, want := range []string{
		"wallets/" + walletTestNS + ".json",
		"data/" + walletTestNS + "/remote-signer-keystores/" + s.material.KeystoreUUID + ".json",
	} {
		if !entries[want] {
			t.Errorf("archive lacks %s", want)
		}
	}
	if !strings.Contains(out, "keystore also under data/"+walletTestNS) {
		t.Errorf("export output does not say the keystore is also under data/:\n%s", out)
	}
	assertNoSecrets(t, out, s.material)
}

// freshHost is an empty config/data pair, as on a new machine.
func freshHost(t *testing.T) *config.Config {
	t.Helper()
	return importTestConfig(t)
}

func TestImport_SkipClusterRestoresKeystoreFromWallets(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, walletTestPass)

	dst := freshHost(t)
	u, out := captureUI()
	if err := Import(dst, ImportOptions{Input: archive, SkipCluster: true, Passphrase: walletTestPass, HasPassFlag: true}, u); err != nil {
		t.Fatalf("Import: %v\n%s", err, out)
	}

	ks := filepath.Join(agentruntime.KeystoreVolumePath(dst, agentruntime.Hermes, walletTestID), s.material.KeystoreUUID+".json")
	data, err := os.ReadFile(ks)
	if err != nil {
		t.Fatalf("keystore not restored: %v\n%s", err, out)
	}
	assertPerm(t, ks, 0o600)
	assertPerm(t, filepath.Dir(ks), 0o700)
	if got := derivedAddress(t, data, s.material.Password); got != s.material.Address {
		t.Fatalf("restored keystore derives %s, want %s", got, s.material.Address)
	}

	text := out.String()
	for _, want := range []string{
		"Wallet " + walletTestNS,
		s.material.Address + " keystore restored from wallets/" + walletTestNS + ".enc to " + ks,
		"fix-permissions init container",
		"obol stack up",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("import output missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "restored inside agent data dirs") {
		t.Errorf("import still claims wallets were restored inside agent data dirs:\n%s", text)
	}
	assertNoSecrets(t, text, s.material)
}

func TestImport_EncryptedWalletWithoutPassphraseIsReported(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, walletTestPass)

	dst := freshHost(t)
	u, out := captureUI() // not a terminal: no prompt
	if err := Import(dst, ImportOptions{Input: archive, SkipCluster: true}, u); err != nil {
		t.Fatalf("Import: %v\n%s", err, out)
	}
	ks := filepath.Join(agentruntime.KeystoreVolumePath(dst, agentruntime.Hermes, walletTestID), s.material.KeystoreUUID+".json")
	if _, err := os.Stat(ks); !os.IsNotExist(err) {
		t.Fatalf("keystore written without a passphrase: %v", err)
	}
	text := out.String()
	for _, want := range []string{"keystore NOT restored", "--passphrase", "obol agent wallet restore " + walletTestID} {
		if !strings.Contains(text, want) {
			t.Errorf("import output missing %q:\n%s", want, text)
		}
	}
}

func TestImport_WrongPassphraseIsReported(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, walletTestPass)

	u, out := captureUI()
	if err := Import(freshHost(t), ImportOptions{Input: archive, SkipCluster: true, Passphrase: "wrong", HasPassFlag: true}, u); err != nil {
		t.Fatalf("Import: %v", err)
	}
	if !strings.Contains(out.String(), "keystore NOT restored") {
		t.Errorf("wrong passphrase not reported:\n%s", out)
	}
}

func TestImport_DataCopyNeedsNoPassphrase(t *testing.T) {
	s := newWalletStack(t)
	archive, _ := exportTo(t, s.cfg, walletTestPass)

	dst := freshHost(t)
	u, out := captureUI()
	if err := Import(dst, ImportOptions{Input: archive, SkipCluster: true}, u); err != nil {
		t.Fatalf("Import: %v", err)
	}
	ks := filepath.Join(agentruntime.KeystoreVolumePath(dst, agentruntime.Hermes, walletTestID), s.material.KeystoreUUID+".json")
	data, err := os.ReadFile(ks)
	if err != nil {
		t.Fatal(err)
	}
	if got := derivedAddress(t, data, s.material.Password); got != s.material.Address {
		t.Fatalf("restored keystore derives %s, want %s", got, s.material.Address)
	}
	if want := s.material.Address + " keystore restored from the archive's data/ copy"; !strings.Contains(out.String(), want) {
		t.Errorf("import output missing %q:\n%s", want, out)
	}
	if strings.Contains(out.String(), "NOT restored") {
		t.Errorf("unexpected failure:\n%s", out)
	}
}

func TestImport_RejectsBackupThatDoesNotMatchConfig(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, "")

	// --cluster-only over a host whose config names a different wallet.
	dst := freshHost(t)
	other, err := keystore.GenerateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	writeWalletConfig(t, dst, other)
	u, out := captureUI()
	if err := Import(dst, ImportOptions{Input: archive, ClusterOnly: true, SkipCluster: true}, u); err != nil {
		t.Fatalf("Import: %v", err)
	}
	dir := agentruntime.KeystoreVolumePath(dst, agentruntime.Hermes, walletTestID)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("mismatching backup written into %s", dir)
	}
	if !strings.Contains(out.String(), "restored config expects "+other.Address) {
		t.Errorf("mismatch not reported:\n%s", out)
	}
}

func TestImport_ClusterOnlyRestoresKeystoreAndRestartsSigner(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, "")

	// Host config already restored (e.g. an earlier --skip-cluster run on a
	// build without this fix), keystore missing, cluster of this stack up.
	dst := importCfg(t)
	dst.DataDir = filepath.Join(t.TempDir(), "data")
	writeWalletConfig(t, dst, s.material)
	h := &importHarness{reachable: []bool{true}, nodes: "k3d-obol-stack-restored-id-server-0"}
	h.install(t)
	var restarted []string
	origRestart := restartRemoteSignerFn
	t.Cleanup(func() { restartRemoteSignerFn = origRestart })
	restartRemoteSignerFn = func(_ *config.Config, ns string) error {
		restarted = append(restarted, ns)
		return nil
	}

	u, out := captureUI()
	if err := Import(dst, ImportOptions{Input: archive, ClusterOnly: true, StackUp: h.stackUp}, u); err != nil {
		t.Fatalf("Import: %v\n%s", err, out)
	}
	ks := filepath.Join(agentruntime.KeystoreVolumePath(dst, agentruntime.Hermes, walletTestID), s.material.KeystoreUUID+".json")
	data, err := os.ReadFile(ks)
	if err != nil {
		t.Fatalf("keystore not restored: %v", err)
	}
	if got := derivedAddress(t, data, s.material.Password); got != s.material.Address {
		t.Fatalf("restored keystore derives %s, want %s", got, s.material.Address)
	}
	if h.stackUps != 0 {
		t.Fatalf("stack up ran %d times on a running cluster", h.stackUps)
	}
	if len(restarted) != 1 || restarted[0] != walletTestNS {
		t.Fatalf("restarted signers = %v, want [%s]", restarted, walletTestNS)
	}
}

func TestImport_SignerNotRestartedWhenImportStartsTheCluster(t *testing.T) {
	s := newWalletStack(t)
	lockKeystoreDir(t, s)
	stubNodeRead(t, s)
	archive, _ := exportTo(t, s.cfg, "")

	dst := importCfg(t)
	dst.DataDir = filepath.Join(t.TempDir(), "data")
	h := &importHarness{reachable: []bool{false, true}}
	h.install(t)
	restarts := 0
	origRestart := restartRemoteSignerFn
	t.Cleanup(func() { restartRemoteSignerFn = origRestart })
	restartRemoteSignerFn = func(*config.Config, string) error { restarts++; return nil }

	u, out := captureUI()
	if err := Import(dst, ImportOptions{Input: archive, Force: true, SkipSync: true, StackUp: h.stackUp}, u); err != nil {
		t.Fatalf("Import: %v\n%s", err, out)
	}
	if h.stackUps != 1 {
		t.Fatalf("stack up ran %d times, want 1", h.stackUps)
	}
	if restarts != 0 {
		t.Fatalf("remote-signer restarted %d times; a freshly started signer loads the keystore itself", restarts)
	}
}

func TestIsWalletNamespace(t *testing.T) {
	for ns, want := range map[string]bool{
		"hermes-obol-agent": true,
		"openclaw-quant":    true,
		"hermes-":           false,
		"agent-sub":         false,
		"../hermes-x":       false,
		".hermes-x":         false,
		"":                  false,
	} {
		if got := isWalletNamespace(ns); got != want {
			t.Errorf("isWalletNamespace(%q) = %v, want %v", ns, got, want)
		}
	}
}
