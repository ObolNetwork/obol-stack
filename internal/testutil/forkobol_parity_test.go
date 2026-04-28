package testutil

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// TestForkObolToken_ParityWithCanonicalOBOL is a build-time parity check that
// catches drift between contracts/fork-obol/src/ForkObolToken.sol and the
// canonical OBOL token deployed at 0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7
// on Ethereum mainnet (verified via Sourcify).
//
// The test is bit-precise: it parses ForkObolToken.sol for the keccak256
// string literals it bakes in, hashes them with the same algorithm Solidity
// uses, and compares the resulting bytes against the canonical values
// independently derived from the OZ ERC20Permit / EIP712 modules and from a
// live `cast call` against mainnet OBOL.
//
// The invariants that matter for x402 Permit2 settlement:
//
//   - EIP-712 domain typehash bytes
//   - Permit struct typehash bytes
//   - Token name + version hashes (the EIP-712 domain inputs)
//   - decimals == 18
//
// Other deltas between ForkObolToken and canonical OBOL (governance via
// ERC20Votes, AccessControl-gated minter, ENS reverse registrar, transfer
// hooks, burn methods) are intentional and orthogonal to settlement.
func TestForkObolToken_ParityWithCanonicalOBOL(t *testing.T) {
	src := readForkObolSource(t)

	// Canonical reference values. The DOMAIN_SEPARATOR result on mainnet was
	// confirmed via `cast call 0x0B010000... 'DOMAIN_SEPARATOR()(bytes32)'`
	// at chain id 1; we don't re-fetch it inside the test so we don't take a
	// network dependency, but the formula reproduces that exact bytes32.
	const (
		canonicalEIP712TypeHash   = "0x8b73c3c69bb8fe3d512ecc4cf759cc79239f7b179b0ffacaa9a75d522b39400f"
		canonicalPermitTypeHash   = "0x6e71edae12b1b97f4d1f60370fef10105fa2faae0126114a169c64845d6126c9"
		canonicalNameHashObol     = "0xc272cbc85e9267f7a7104c8745c6b9edcd2dcf6627beaed25edd4cf95159d5fc"
		canonicalVersionHashOne   = "0xc89efdaa54c0f20c7adf612882df0950f5a951637e0307cdcb4c672f298b8bc6"
		canonicalDomainSepMainnet = "0x5a3cd81e467dcdfe5d4ed4383d31f23bd6ce41b7be43812c5554ba9f7d949432"
		canonicalDomainSepAddress = "0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7"
		canonicalDomainSepChainID = uint64(1)
	)

	// 1. The EIP-712 domain typehash literal.
	mustMatchKeccak(t, src,
		`EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)`,
		canonicalEIP712TypeHash, "EIP-712 domain typehash")

	// 2. The Permit struct typehash literal.
	mustMatchKeccak(t, src,
		`Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)`,
		canonicalPermitTypeHash, "Permit struct typehash")

	// 3. Token name hash. ForkObolToken hardcodes keccak256(bytes("Obol Network")).
	if got := keccakHex([]byte("Obol Network")); got != canonicalNameHashObol {
		t.Fatalf("name hash drift: got %s, canonical %s", got, canonicalNameHashObol)
	}
	if !strings.Contains(src, `keccak256(bytes("Obol Network"))`) {
		t.Fatalf("ForkObolToken.sol no longer hashes the literal \"Obol Network\" — domain separator will diverge from canonical OBOL")
	}

	// 4. Version hash. ForkObolToken hardcodes keccak256(bytes("1")).
	if got := keccakHex([]byte("1")); got != canonicalVersionHashOne {
		t.Fatalf("version hash drift: got %s, canonical %s", got, canonicalVersionHashOne)
	}
	if !strings.Contains(src, `keccak256(bytes("1"))`) {
		t.Fatalf("ForkObolToken.sol no longer hashes the literal \"1\" — version mismatch with canonical OBOL")
	}

	// 5. decimals must be 18 — every signed amount is decimal-shifted by the
	//    buyer at signing time. Drift here breaks every Permit2 payload.
	if !strings.Contains(src, "uint8 public constant decimals = 18;") {
		t.Fatalf("ForkObolToken.sol decimals != 18; canonical OBOL is 18")
	}

	// 6. Sanity: rebuild the domain separator the canonical OBOL would produce
	//    if it lived at 0x0B01... on chain id 1, and assert it matches the
	//    bytes32 returned by `cast call DOMAIN_SEPARATOR()` on mainnet. This
	//    is what the buyer signs against; if any of the four inputs drifts
	//    (typehash, name hash, version hash, address+chain) the bytes change.
	got := buildDomainSeparator(
		mustHex32(t, canonicalEIP712TypeHash),
		mustHex32(t, canonicalNameHashObol),
		mustHex32(t, canonicalVersionHashOne),
		canonicalDomainSepChainID,
		mustHexAddr(t, canonicalDomainSepAddress),
	)
	if got != canonicalDomainSepMainnet {
		t.Fatalf("domain separator formula drift: built %s, mainnet returns %s", got, canonicalDomainSepMainnet)
	}
}

func readForkObolSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..")
	src := filepath.Join(repoRoot, "contracts", "fork-obol", "src", "ForkObolToken.sol")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read ForkObolToken.sol: %v", err)
	}
	return string(body)
}

// mustMatchKeccak finds keccak256("LITERAL") inside ForkObolToken.sol and
// asserts that keccak256 of that literal equals the canonical bytes32.
func mustMatchKeccak(t *testing.T, src, literal, canonicalHex, label string) {
	t.Helper()
	pattern := regexp.MustCompile(`keccak256\("` + regexp.QuoteMeta(literal) + `"\)`)
	if !pattern.MatchString(src) {
		t.Fatalf("%s: ForkObolToken.sol does not contain keccak256(%q)", label, literal)
	}
	if got := keccakHex([]byte(literal)); got != canonicalHex {
		t.Fatalf("%s: keccak drift — got %s, canonical %s", label, got, canonicalHex)
	}
}

func keccakHex(data []byte) string {
	return "0x" + hex.EncodeToString(crypto.Keccak256(data))
}

func mustHex32(t *testing.T, s string) [32]byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != 32 {
		t.Fatalf("bad bytes32: %s", s)
	}
	var out [32]byte
	copy(out[:], b)
	return out
}

func mustHexAddr(t *testing.T, s string) [20]byte {
	t.Helper()
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != 20 {
		t.Fatalf("bad address: %s", s)
	}
	var out [20]byte
	copy(out[:], b)
	return out
}

// buildDomainSeparator computes the canonical OZ-style EIP-712 domain
// separator: keccak256(abi.encode(typeHash, nameHash, versionHash, chainId,
// address)). The encoding is exactly 5*32 bytes (uint256 chainId, address
// left-padded to 32 bytes), matching what Solidity's abi.encode emits.
func buildDomainSeparator(typeHash, nameHash, versionHash [32]byte, chainID uint64, addr [20]byte) string {
	buf := make([]byte, 0, 5*32)
	buf = append(buf, typeHash[:]...)
	buf = append(buf, nameHash[:]...)
	buf = append(buf, versionHash[:]...)

	var chainBuf [32]byte
	// big-endian uint256 — leftmost 24 bytes zero, then 8 bytes of chainID.
	for i := 0; i < 8; i++ {
		chainBuf[31-i] = byte(chainID >> (8 * uint(i)))
	}
	buf = append(buf, chainBuf[:]...)

	var addrBuf [32]byte
	copy(addrBuf[12:], addr[:])
	buf = append(buf, addrBuf[:]...)

	return "0x" + hex.EncodeToString(crypto.Keccak256(buf))
}
