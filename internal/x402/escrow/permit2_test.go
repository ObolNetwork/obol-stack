package escrow

import (
	"crypto/ecdsa"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Anvil dev key 0 — fixed so signatures and digests are deterministic.
const anvilKey0 = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

var testSpender = common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8") // anvil #1

func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.HexToECDSA(anvilKey0)
	if err != nil {
		t.Fatalf("parse test key: %v", err)
	}
	return key
}

// goldenVoucher is the fixed voucher every pinned value derives from.
// Deadline 1893456000 = 2030-01-01T00:00:00Z.
func goldenVoucher(t *testing.T) (Permit2Voucher, *ecdsa.PrivateKey) {
	t.Helper()
	key := testKey(t)
	return Permit2Voucher{
		Owner:    crypto.PubkeyToAddress(key.PublicKey).Hex(), // 0xf39F...2266
		Token:    "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
		Network:  "base-sepolia",
		Spender:  testSpender.Hex(),
		Nonce:    "1",
		Deadline: 1893456000,
		Recipients: []BatchRecipient{
			{Address: "0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC", Amount: "1000"},
			{Address: "0x90F79bf6EB2c4f870365E785982E1f101E93b906", Amount: "2500"},
		},
	}, key
}

func TestChainIDForNetwork(t *testing.T) {
	for _, tc := range []struct {
		network string
		want    int64
	}{
		{"base-sepolia", 84532},
		{"base", 8453},
		{"eip155:84532", 84532},
		{"EIP155:31337", 31337}, // arbitrary CAIP-2 ids pass through
		{"ethereum", 1},
	} {
		got, err := ChainIDForNetwork(tc.network)
		if err != nil {
			t.Fatalf("ChainIDForNetwork(%q): %v", tc.network, err)
		}
		if got.Int64() != tc.want {
			t.Errorf("ChainIDForNetwork(%q) = %s, want %d", tc.network, got, tc.want)
		}
	}
	if _, err := ChainIDForNetwork("not-a-chain"); err == nil {
		t.Error("ChainIDForNetwork(not-a-chain) should fail")
	}
	if _, err := ChainIDForNetwork(""); err == nil {
		t.Error("ChainIDForNetwork(empty) should fail")
	}
}

func TestSignVoucher_VerifyRoundTrip(t *testing.T) {
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	chainID := big.NewInt(84532)

	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatalf("SignVoucher: %v", err)
	}
	if v.Signature == "" || !strings.HasPrefix(v.Signature, "0x") || len(v.Signature) != 132 {
		t.Fatalf("Signature = %q, want 65-byte 0x-hex", v.Signature)
	}
	if err := VerifyVoucher(v, chainID, testSpender); err != nil {
		t.Fatalf("VerifyVoucher: %v", err)
	}
}

func TestSignVoucher_FillsOwnerAndRejectsMismatch(t *testing.T) {
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	chainID := big.NewInt(84532)

	v.Owner = ""
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatalf("SignVoucher with empty owner: %v", err)
	}
	want := crypto.PubkeyToAddress(key.PublicKey)
	if common.HexToAddress(v.Owner) != want {
		t.Errorf("Owner = %s, want %s", v.Owner, want.Hex())
	}

	v.Owner = testSpender.Hex() // not the key's address
	if err := SignVoucher(&v, chainID, key); err == nil {
		t.Error("SignVoucher should reject owner/key mismatch")
	}
}

func TestVerifyVoucher_WrongSpender(t *testing.T) {
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	chainID := big.NewInt(84532)
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}

	other := crypto.PubkeyToAddress(testKey(t).PublicKey) // owner, not spender
	if err := VerifyVoucher(v, chainID, other); err == nil || !strings.Contains(err.Error(), "spender") {
		t.Fatalf("VerifyVoucher with wrong spender = %v, want spender binding error", err)
	}
	if err := VerifyVoucher(v, chainID, common.Address{}); err == nil {
		t.Fatal("VerifyVoucher with zero expected spender should fail")
	}
}

func TestVerifyVoucher_Expired(t *testing.T) {
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(-time.Minute).Unix()
	chainID := big.NewInt(84532)
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}
	if err := VerifyVoucher(v, chainID, testSpender); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("VerifyVoucher(expired) = %v, want expiry error", err)
	}
}

func TestVerifyVoucher_TamperedVoucherFails(t *testing.T) {
	chainID := big.NewInt(84532)

	tamper := []struct {
		name   string
		mutate func(*Permit2Voucher)
	}{
		{"amount", func(v *Permit2Voucher) { v.Recipients[0].Amount = "999999" }},
		{"nonce", func(v *Permit2Voucher) { v.Nonce = "2" }},
		{"deadline", func(v *Permit2Voucher) { v.Deadline += 60 }},
		{"token", func(v *Permit2Voucher) { v.Token = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913" }},
	}
	for _, tc := range tamper {
		t.Run(tc.name, func(t *testing.T) {
			v, key := goldenVoucher(t)
			v.Deadline = time.Now().Add(time.Hour).Unix()
			if err := SignVoucher(&v, chainID, key); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&v)
			if err := VerifyVoucher(v, chainID, testSpender); err == nil {
				t.Fatalf("VerifyVoucher should fail after tampering with %s", tc.name)
			}
		})
	}

	// Wrong chain id also breaks the signature (domain separator changes).
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}
	if err := VerifyVoucher(v, big.NewInt(8453), testSpender); err == nil {
		t.Fatal("VerifyVoucher should fail on a different chain id")
	}
}

// TestVoucherRecipientAddressIsPolicyBoundNotSignatureBound documents a known
// property of standard (non-witness) Permit2 SignatureTransfer: the signature
// commits to the (token, amount) seats, spender, nonce, and deadline — NOT to
// recipient addresses, which live only in transferDetails at execution time.
// Recipient binding is therefore facilitator POLICY (capture pays only the
// stored voucher's seats, transported under the bearer-token reserve), not
// cryptography. Binding addresses into the signature would require the
// PermitBatchWitnessTransferFrom variant.
func TestVoucherRecipientAddressIsPolicyBoundNotSignatureBound(t *testing.T) {
	chainID := big.NewInt(84532)
	v, key := goldenVoucher(t)
	v.Deadline = time.Now().Add(time.Hour).Unix()
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}
	v.Recipients[1].Address = testSpender.Hex() // same amount, different payee
	if err := VerifyVoucher(v, chainID, testSpender); err != nil {
		t.Fatalf("recipient address is not part of the Permit2 digest; verify = %v", err)
	}
}

func TestVerifyVoucher_FieldValidation(t *testing.T) {
	chainID := big.NewInt(84532)
	cases := []struct {
		name   string
		mutate func(*Permit2Voucher)
	}{
		{"zero amount", func(v *Permit2Voucher) { v.Recipients[0].Amount = "0" }},
		{"negative amount", func(v *Permit2Voucher) { v.Recipients[0].Amount = "-5" }},
		{"non-numeric amount", func(v *Permit2Voucher) { v.Recipients[0].Amount = "1.5 USDC" }},
		{"no recipients", func(v *Permit2Voucher) { v.Recipients = nil }},
		{"bad owner", func(v *Permit2Voucher) { v.Owner = "owner" }},
		{"bad nonce", func(v *Permit2Voucher) { v.Nonce = "0xzz" }},
		{"zero deadline", func(v *Permit2Voucher) { v.Deadline = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, key := goldenVoucher(t)
			v.Deadline = time.Now().Add(time.Hour).Unix()
			if err := SignVoucher(&v, chainID, key); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&v)
			if err := VerifyVoucher(v, chainID, testSpender); err == nil {
				t.Fatalf("VerifyVoucher should reject %s", tc.name)
			}
		})
	}
}

// TestHashVoucher_GoldenAndManualReconstruction pins the canonical digest and
// independently reconstructs it with raw keccak over Permit2's PermitHash
// semantics — proving the apitypes encoding matches the on-chain library
// (domain WITHOUT version, nested TokenPermissions[] array of struct hashes).
func TestHashVoucher_GoldenAndManualReconstruction(t *testing.T) {
	v, _ := goldenVoucher(t)
	chainID := big.NewInt(84532)

	got, err := HashVoucher(v, chainID)
	if err != nil {
		t.Fatalf("HashVoucher: %v", err)
	}
	const golden = "0x352592eb204c815305c91afb79b1136fe4714297bd5cbb0c6ed3fe75fa8e6a75"
	if got.Hex() != golden {
		t.Errorf("HashVoucher = %s, want pinned %s", got.Hex(), golden)
	}

	// Manual reconstruction, mirroring permit2/src/libraries/PermitHash.sol.
	pad := func(b []byte) []byte { return common.LeftPadBytes(b, 32) }
	domainTypeHash := crypto.Keccak256([]byte("EIP712Domain(string name,uint256 chainId,address verifyingContract)"))
	domainSep := crypto.Keccak256(
		domainTypeHash,
		crypto.Keccak256([]byte("Permit2")),
		pad(chainID.Bytes()),
		pad(common.HexToAddress(Permit2Address).Bytes()),
	)

	tokenPermTypeHash := crypto.Keccak256([]byte("TokenPermissions(address token,uint256 amount)"))
	var permHashes []byte
	for _, r := range v.Recipients {
		amount, ok := new(big.Int).SetString(r.Amount, 10)
		if !ok {
			t.Fatalf("amount %q", r.Amount)
		}
		permHashes = append(permHashes, crypto.Keccak256(
			tokenPermTypeHash,
			pad(common.HexToAddress(v.Token).Bytes()),
			pad(amount.Bytes()),
		)...)
	}
	permittedHash := crypto.Keccak256(permHashes)

	batchTypeHash := crypto.Keccak256([]byte(
		"PermitBatchTransferFrom(TokenPermissions[] permitted,address spender,uint256 nonce,uint256 deadline)TokenPermissions(address token,uint256 amount)",
	))
	nonce, _ := new(big.Int).SetString(v.Nonce, 10)
	structHash := crypto.Keccak256(
		batchTypeHash,
		permittedHash,
		pad(common.HexToAddress(v.Spender).Bytes()),
		pad(nonce.Bytes()),
		pad(big.NewInt(v.Deadline).Bytes()),
	)

	manual := crypto.Keccak256([]byte("\x19\x01"), domainSep, structHash)
	if common.BytesToHash(manual) != got {
		t.Errorf("manual PermitHash reconstruction %x != HashVoucher %s", manual, got.Hex())
	}
}

func TestVoucherTypedData_RemotePayloadShape(t *testing.T) {
	v, _ := goldenVoucher(t)
	chainID := big.NewInt(84532)

	typed, remote, err := VoucherTypedData(v, chainID)
	if err != nil {
		t.Fatalf("VoucherTypedData: %v", err)
	}
	if typed.PrimaryType != "PermitBatchTransferFrom" || remote.PrimaryType != "PermitBatchTransferFrom" {
		t.Errorf("primary types = %q / %q", typed.PrimaryType, remote.PrimaryType)
	}

	// Permit2's domain has NO version field.
	if _, ok := remote.Domain["version"]; ok {
		t.Error("remote domain must not carry a version field (Permit2 omits it)")
	}
	for _, f := range remote.Types["EIP712Domain"] {
		if f.Name == "version" {
			t.Error("EIP712Domain type must not declare version")
		}
	}
	if remote.Domain["name"] != "Permit2" {
		t.Errorf("domain name = %v", remote.Domain["name"])
	}
	if remote.Domain["chainId"] != "84532" {
		t.Errorf("domain chainId = %v, want decimal string", remote.Domain["chainId"])
	}
	if remote.Domain["verifyingContract"] != Permit2Address {
		t.Errorf("verifyingContract = %v", remote.Domain["verifyingContract"])
	}

	permitted, ok := remote.Message["permitted"].([]interface{})
	if !ok || len(permitted) != len(v.Recipients) {
		t.Fatalf("permitted = %#v, want one entry per recipient seat", remote.Message["permitted"])
	}

	if _, _, err := VoucherTypedData(v, nil); err == nil {
		t.Error("VoucherTypedData(nil chainID) should fail")
	}
}
