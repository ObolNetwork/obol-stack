package hermes

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/walletbackup"
)

// HostWallet locates one host-side remote-signer wallet: its metadata
// (wallet.json) and keystore password (values-remote-signer.yaml) under the
// instance's deployment dir, its V3 keystore under the namespace's
// remote-signer-keystores data dir. The layout is the same for Hermes
// instances and OpenClaw instances left over from <= v0.15.
type HostWallet struct {
	Namespace   string // hermes-<id> / openclaw-<id>
	Instance    string // instance id recorded in the backup file
	DeployDir   string
	KeystoreDir string
}

// HermesHostWallet returns the HostWallet of Hermes instance id.
func HermesHostWallet(cfg *config.Config, id string) HostWallet {
	return HostWallet{
		Namespace:   agentruntime.Namespace(agentruntime.Hermes, id),
		Instance:    id,
		DeployDir:   DeploymentPath(cfg, id),
		KeystoreDir: agentruntime.KeystoreVolumePath(cfg, agentruntime.Hermes, id),
	}
}

// KeystoreRoute records how a keystore file was read for a backup.
type KeystoreRoute string

const (
	// KeystoreRouteHost: read directly from the host data dir.
	KeystoreRouteHost KeystoreRoute = "host"
	// KeystoreRouteK3dNode: the host copy is not readable by this user (on
	// Linux the remote-signer pod owns it as UID 65532, mode 0700/0600), so
	// it was read through the k3d server node container instead.
	KeystoreRouteK3dNode KeystoreRoute = "k3d-node"
)

// BuildHostWalletBackup assembles the walletbackup.File for one host wallet
// without writing it anywhere. It reports which route read the keystore.
func BuildHostWalletBackup(cfg *config.Config, w HostWallet) (*walletbackup.File, KeystoreRoute, error) {
	wallet, err := ReadWalletMetadata(w.DeployDir)
	if err != nil {
		return nil, "", fmt.Errorf("no wallet found for instance %q: %w", w.Instance, err)
	}

	keystorePath := filepath.Join(w.KeystoreDir, wallet.KeystoreUUID+".json")
	keystoreData, route, err := readKeystoreFileForBackup(cfg, keystorePath)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read keystore file: %w", err)
	}

	password, err := walletbackup.ReadKeystorePassword(w.DeployDir)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read keystore password: %w", err)
	}

	return &walletbackup.File{
		Version:  walletbackup.Version,
		Instance: w.Instance,
		Wallets: []walletbackup.Wallet{{
			Address:          wallet.Address,
			PublicKey:        wallet.PublicKey,
			KeystoreUUID:     wallet.KeystoreUUID,
			CreatedAt:        wallet.CreatedAt,
			Keystore:         keystoreData,
			KeystorePassword: password,
		}},
	}, route, nil
}

// readKeystoreFileForBackup reads a keystore from the host data dir, falling
// back to the k3d server node when the host copy is not readable by this
// user (the remote-signer pod owns it).
func readKeystoreFileForBackup(cfg *config.Config, keystorePath string) ([]byte, KeystoreRoute, error) {
	data, err := readHostKeystoreFileFn(keystorePath)
	if err == nil {
		return data, KeystoreRouteHost, nil
	}
	if !os.IsPermission(err) || !isK3dBackend(cfg) {
		return nil, "", err
	}

	data, fallbackErr := k3dNodeExecOutputFn(cfg, keystorePath, "cat {}")
	if fallbackErr != nil {
		return nil, "", fmt.Errorf("%w; k3d node read fallback failed (is the cluster running?): %w", err, fallbackErr)
	}
	return data, KeystoreRouteK3dNode, nil
}
