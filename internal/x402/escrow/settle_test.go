package escrow

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBuildTransferDetails_FullAndSubset(t *testing.T) {
	v, _ := goldenVoucher(t)

	// Full capture: every seat paid.
	full, err := BuildTransferDetails(v, v.Recipients)
	if err != nil {
		t.Fatalf("full capture: %v", err)
	}
	if len(full) != 2 || full[0].Amount.Cmp(big.NewInt(1000)) != 0 || full[1].Amount.Cmp(big.NewInt(2500)) != 0 {
		t.Fatalf("full details = %+v", full)
	}

	// Subset: only the second seat paid; the first stays in the array at 0
	// (index-wise pairing with permitted[i], never shortened).
	subset, err := BuildTransferDetails(v, []BatchRecipient{{Address: strings.ToLower(v.Recipients[1].Address), Amount: "2500"}})
	if err != nil {
		t.Fatalf("subset capture: %v", err)
	}
	if len(subset) != 2 {
		t.Fatalf("subset details length = %d, want 2 (omitted seats stay at zero)", len(subset))
	}
	if subset[0].Amount.Sign() != 0 {
		t.Errorf("omitted seat amount = %s, want 0", subset[0].Amount)
	}
	if subset[0].To != common.HexToAddress(v.Recipients[0].Address) {
		t.Errorf("omitted seat To = %s, want voucher seat address", subset[0].To.Hex())
	}
	if subset[1].Amount.Cmp(big.NewInt(2500)) != 0 {
		t.Errorf("paid seat amount = %s, want 2500", subset[1].Amount)
	}
}

func TestBuildTransferDetails_Errors(t *testing.T) {
	v, _ := goldenVoucher(t)

	// Recipient not in the voucher.
	if _, err := BuildTransferDetails(v, []BatchRecipient{{Address: testSpender.Hex(), Amount: "1000"}}); err == nil {
		t.Error("unknown recipient should fail")
	}
	// Amount differs from the signed seat amount.
	if _, err := BuildTransferDetails(v, []BatchRecipient{{Address: v.Recipients[0].Address, Amount: "999"}}); err == nil {
		t.Error("amount mismatch should fail")
	}
	// More than the signed amount.
	if _, err := BuildTransferDetails(v, []BatchRecipient{{Address: v.Recipients[0].Address, Amount: "100000"}}); err == nil {
		t.Error("over-request should fail")
	}
	// Empty request.
	if _, err := BuildTransferDetails(v, nil); err == nil {
		t.Error("empty request should fail")
	}
	// Same seat requested twice (only one seat exists at that address).
	if _, err := BuildTransferDetails(v, []BatchRecipient{
		{Address: v.Recipients[0].Address, Amount: "1000"},
		{Address: v.Recipients[0].Address, Amount: "1000"},
	}); err == nil {
		t.Error("double-spending one seat should fail")
	}
}

func TestBuildTransferDetails_DuplicateSeats(t *testing.T) {
	v, _ := goldenVoucher(t)
	addr := v.Recipients[0].Address
	v.Recipients = []BatchRecipient{
		{Address: addr, Amount: "1000"},
		{Address: addr, Amount: "1000"},
	}

	// Two identical seats: requesting twice consumes both.
	details, err := BuildTransferDetails(v, []BatchRecipient{
		{Address: addr, Amount: "1000"},
		{Address: addr, Amount: "1000"},
	})
	if err != nil {
		t.Fatalf("duplicate seats: %v", err)
	}
	if details[0].Amount.Cmp(big.NewInt(1000)) != 0 || details[1].Amount.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("details = %+v", details)
	}

	// Requesting once pays one seat, leaves the other at zero.
	one, err := BuildTransferDetails(v, []BatchRecipient{{Address: addr, Amount: "1000"}})
	if err != nil {
		t.Fatal(err)
	}
	if one[0].Amount.Cmp(big.NewInt(1000)) != 0 || one[1].Amount.Sign() != 0 {
		t.Fatalf("one-seat details = %+v", one)
	}
}

// goldenCalldata is the exact permitTransferFrom calldata for goldenVoucher
// signed with anvil key 0 on chain 84532, capturing ONLY the first seat —
// the second seat appears index-wise with requestedAmount 0.
// Selector edd9444b = keccak("permitTransferFrom(((address,uint256)[],uint256,uint256),(address,uint256)[],address,bytes)")[:4].
const goldenCalldata = "edd9444b" +
	"0000000000000000000000000000000000000000000000000000000000000080" + // permit tuple offset
	"0000000000000000000000000000000000000000000000000000000000000180" + // transferDetails offset
	"000000000000000000000000f39fd6e51aad88f6f4ce6ab8827279cfffb92266" + // owner
	"0000000000000000000000000000000000000000000000000000000000000220" + // signature offset
	"0000000000000000000000000000000000000000000000000000000000000060" + // permit.permitted offset
	"0000000000000000000000000000000000000000000000000000000000000001" + // nonce = 1
	"0000000000000000000000000000000000000000000000000000000070dbd880" + // deadline = 1893456000
	"0000000000000000000000000000000000000000000000000000000000000002" + // permitted length
	"000000000000000000000000036cbd53842c5426634e7929541ec2318f3dcf7e" + // permitted[0].token
	"00000000000000000000000000000000000000000000000000000000000003e8" + // permitted[0].amount = 1000
	"000000000000000000000000036cbd53842c5426634e7929541ec2318f3dcf7e" + // permitted[1].token
	"00000000000000000000000000000000000000000000000000000000000009c4" + // permitted[1].amount = 2500
	"0000000000000000000000000000000000000000000000000000000000000002" + // transferDetails length
	"0000000000000000000000003c44cdddb6a900fa2b585dd299e03d12fa4293bc" + // details[0].to
	"00000000000000000000000000000000000000000000000000000000000003e8" + // details[0].requestedAmount = 1000
	"00000000000000000000000090f79bf6eb2c4f870365e785982e1f101e93b906" + // details[1].to (omitted seat)
	"0000000000000000000000000000000000000000000000000000000000000000" + // details[1].requestedAmount = 0
	"0000000000000000000000000000000000000000000000000000000000000041" + // signature length (65)
	"8eb05e00fa60ef44b63ec69978e25ce2d2f3a142ce3d603e89b4e8c06811555a" +
	"7c41076a83d3f1b24405b7418cb4041b269325c2f4fae161f01460aab0cb6f40" +
	"1c00000000000000000000000000000000000000000000000000000000000000"

func TestBuildPermitTransferFromCalldata_Golden(t *testing.T) {
	v, key := goldenVoucher(t)
	chainID := big.NewInt(84532)
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}

	details, err := BuildTransferDetails(v, []BatchRecipient{{Address: v.Recipients[0].Address, Amount: "1000"}})
	if err != nil {
		t.Fatal(err)
	}
	calldata, err := BuildPermitTransferFromCalldata(v, details)
	if err != nil {
		t.Fatalf("BuildPermitTransferFromCalldata: %v", err)
	}
	if got := hex.EncodeToString(calldata); got != goldenCalldata {
		t.Errorf("calldata mismatch:\n got %s\nwant %s", got, goldenCalldata)
	}

	// Independent cross-check: the ABI fragment must produce the canonical
	// batch permitTransferFrom selector.
	wantSelector := crypto.Keccak256([]byte("permitTransferFrom(((address,uint256)[],uint256,uint256),(address,uint256)[],address,bytes)"))[:4]
	if hex.EncodeToString(calldata[:4]) != hex.EncodeToString(wantSelector) {
		t.Errorf("selector = %x, want %x", calldata[:4], wantSelector)
	}
}

func TestBuildPermitTransferFromCalldata_LengthInvariant(t *testing.T) {
	v, key := goldenVoucher(t)
	chainID := big.NewInt(84532)
	if err := SignVoucher(&v, chainID, key); err != nil {
		t.Fatal(err)
	}

	// transferDetails must pair index-wise with permitted — shortening it is
	// a hard error, not a silent re-pairing.
	short := []TransferDetail{{To: common.HexToAddress(v.Recipients[0].Address), Amount: big.NewInt(1000)}}
	if _, err := BuildPermitTransferFromCalldata(v, short); err == nil {
		t.Error("shortened transferDetails should fail")
	}
}

func TestErpcNetworkSuffix(t *testing.T) {
	for _, tc := range []struct{ network, want string }{
		{"ethereum", "mainnet"}, // erc8004 eRPC alias convention
		{"base-sepolia", "base-sepolia"},
		{"base", "base"},
		{"eip155:84532", "base-sepolia"}, // CAIP-2 falls through to the x402 registry
		{"my-custom-net", "my-custom-net"},
	} {
		if got := erpcNetworkSuffix(tc.network); got != tc.want {
			t.Errorf("erpcNetworkSuffix(%q) = %q, want %q", tc.network, got, tc.want)
		}
	}
}
