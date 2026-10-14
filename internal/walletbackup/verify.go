package walletbackup

import (
	"errors"
	"fmt"
	"strings"

	gethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
)

// VerifyKeystore decrypts the wallet's V3 keystore with its recorded
// password and checks that the derived address is the recorded one. The
// decrypted key never leaves this function; only addresses appear in the
// returned error. go-ethereum's decoder is used so keystores imported from
// other tools (pbkdf2 KDF) verify too.
func (w Wallet) VerifyKeystore() error {
	if len(w.Keystore) == 0 {
		return errors.New("backup carries no keystore")
	}
	key, err := gethkeystore.DecryptKey(w.Keystore, w.KeystorePassword)
	if err != nil {
		return fmt.Errorf("keystore does not decrypt with its recorded password: %w", err)
	}
	derived := key.Address.Hex()
	if !strings.EqualFold(derived, w.Address) {
		return fmt.Errorf("keystore decrypts to %s, backup records %s", derived, w.Address)
	}
	return nil
}
