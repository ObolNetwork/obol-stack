package walletbackup

import (
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/keystore"
)

func TestWalletVerifyKeystore(t *testing.T) {
	m, err := keystore.GenerateInMemory()
	if err != nil {
		t.Fatal(err)
	}
	w := Wallet{Address: m.Address, Keystore: m.KeystoreJSON, KeystorePassword: m.Password}
	if err := w.VerifyKeystore(); err != nil {
		t.Fatalf("valid keystore rejected: %v", err)
	}

	wrongAddr := w
	wrongAddr.Address = "0x1111111111111111111111111111111111111111"
	if err := wrongAddr.VerifyKeystore(); err == nil || !strings.Contains(err.Error(), m.Address) {
		t.Fatalf("address mismatch not reported with the derived address: %v", err)
	}

	wrongPass := w
	wrongPass.KeystorePassword = "not-the-password"
	err = wrongPass.VerifyKeystore()
	if err == nil {
		t.Fatal("wrong password accepted")
	}
	if strings.Contains(err.Error(), m.Password) {
		t.Fatal("error leaks the keystore password")
	}

	if err := (Wallet{Address: m.Address}).VerifyKeystore(); err == nil {
		t.Fatal("empty keystore accepted")
	}
}
