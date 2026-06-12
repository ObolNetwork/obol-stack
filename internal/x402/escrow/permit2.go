package escrow

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/x402"
)

// Permit2Address is the canonical Uniswap Permit2 deployment, CREATE2-deployed
// at the same address on every EVM chain.
const Permit2Address = "0x000000000022D473030F116dDEE9F6B43aC78BA3"

// ChainIDForNetwork resolves a chain alias ("base-sepolia") or CAIP-2 id
// ("eip155:84532") to its EIP-155 chain ID. Aliases route through the x402
// chain registry so both legacy and CAIP-2 forms work (see pitfall: both
// forms must resolve or paid routes silently 404).
func ChainIDForNetwork(network string) (*big.Int, error) {
	n := strings.TrimSpace(network)
	if n == "" {
		return nil, fmt.Errorf("permit2: empty network")
	}
	if rest, ok := strings.CutPrefix(strings.ToLower(n), "eip155:"); ok {
		id, parsed := new(big.Int).SetString(rest, 10)
		if !parsed || id.Sign() <= 0 {
			return nil, fmt.Errorf("permit2: invalid CAIP-2 network %q", network)
		}
		return id, nil
	}
	info, err := x402.ResolveChainInfo(n)
	if err != nil {
		return nil, fmt.Errorf("permit2: resolve network %q: %w", network, err)
	}
	rest := strings.TrimPrefix(info.CAIP2Network, "eip155:")
	id, parsed := new(big.Int).SetString(rest, 10)
	if !parsed || id.Sign() <= 0 {
		return nil, fmt.Errorf("permit2: chain registry returned invalid CAIP-2 id %q for %q", info.CAIP2Network, network)
	}
	return id, nil
}

// voucherTypes is the EIP-712 type set for Permit2 SignatureTransfer
// PermitBatchTransferFrom. The domain deliberately has NO version field —
// Permit2's EIP712.sol hashes
// keccak256("EIP712Domain(string name,uint256 chainId,address verifyingContract)")
// with name "Permit2" and nothing else.
func voucherTypes() apitypes.Types {
	return apitypes.Types{
		"EIP712Domain": {
			{Name: "name", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "verifyingContract", Type: "address"},
		},
		"PermitBatchTransferFrom": {
			{Name: "permitted", Type: "TokenPermissions[]"},
			{Name: "spender", Type: "address"},
			{Name: "nonce", Type: "uint256"},
			{Name: "deadline", Type: "uint256"},
		},
		"TokenPermissions": {
			{Name: "token", Type: "address"},
			{Name: "amount", Type: "uint256"},
		},
	}
}

// parseUint256 parses a non-negative decimal uint256 string.
func parseUint256(field, s string) (*big.Int, error) {
	v, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
	if !ok || v.Sign() < 0 || v.BitLen() > 256 {
		return nil, fmt.Errorf("permit2: %s %q is not a decimal uint256", field, s)
	}
	return v, nil
}

// parsePositiveAmount parses a strictly positive decimal uint256 amount.
func parsePositiveAmount(field, s string) (*big.Int, error) {
	v, err := parseUint256(field, s)
	if err != nil {
		return nil, err
	}
	if v.Sign() <= 0 {
		return nil, fmt.Errorf("permit2: %s %q must be a positive integer", field, s)
	}
	return v, nil
}

// validateVoucherFields checks every field needed to hash the voucher (it does
// NOT check signature or deadline-in-future — that is VerifyVoucher's job).
func validateVoucherFields(v Permit2Voucher) error {
	if !common.IsHexAddress(v.Owner) {
		return fmt.Errorf("permit2: invalid owner address %q", v.Owner)
	}
	if !common.IsHexAddress(v.Token) {
		return fmt.Errorf("permit2: invalid token address %q", v.Token)
	}
	if !common.IsHexAddress(v.Spender) {
		return fmt.Errorf("permit2: invalid spender address %q", v.Spender)
	}
	if _, err := parseUint256("nonce", v.Nonce); err != nil {
		return err
	}
	if v.Deadline <= 0 {
		return fmt.Errorf("permit2: deadline %d must be a positive unix timestamp", v.Deadline)
	}
	if len(v.Recipients) == 0 {
		return fmt.Errorf("permit2: voucher has no recipients")
	}
	for i, r := range v.Recipients {
		if !common.IsHexAddress(r.Address) {
			return fmt.Errorf("permit2: recipient %d has invalid address %q", i, r.Address)
		}
		if _, err := parsePositiveAmount(fmt.Sprintf("recipient %d amount", i), r.Amount); err != nil {
			return err
		}
	}
	return nil
}

// VoucherTypedData builds the EIP-712 payload for a voucher in both the
// go-ethereum apitypes form (canonical digest, local signing) and the
// erc8004.EIP712TypedData form (RemoteSigner.SignTypedData wire payload).
// permitted[i] pairs with v.Recipients[i]: one TokenPermissions entry per
// recipient seat, all on the same token. The chainId in the remote-signer
// domain is a decimal string (math.HexOrDecimal256 accepts both forms).
func VoucherTypedData(v Permit2Voucher, chainID *big.Int) (apitypes.TypedData, erc8004.EIP712TypedData, error) {
	if chainID == nil || chainID.Sign() <= 0 {
		return apitypes.TypedData{}, erc8004.EIP712TypedData{}, fmt.Errorf("permit2: chainID must be positive")
	}
	if err := validateVoucherFields(v); err != nil {
		return apitypes.TypedData{}, erc8004.EIP712TypedData{}, err
	}

	token := common.HexToAddress(v.Token).Hex()
	permitted := make([]interface{}, len(v.Recipients))
	for i, r := range v.Recipients {
		permitted[i] = map[string]interface{}{
			"token":  token,
			"amount": r.Amount,
		}
	}
	message := map[string]interface{}{
		"permitted": permitted,
		"spender":   common.HexToAddress(v.Spender).Hex(),
		"nonce":     v.Nonce,
		"deadline":  strconv.FormatInt(v.Deadline, 10),
	}

	typed := apitypes.TypedData{
		Types:       voucherTypes(),
		PrimaryType: "PermitBatchTransferFrom",
		Domain: apitypes.TypedDataDomain{
			Name:              "Permit2",
			ChainId:           (*math.HexOrDecimal256)(new(big.Int).Set(chainID)),
			VerifyingContract: Permit2Address,
		},
		Message: message,
	}

	remoteTypes := make(map[string][]erc8004.EIP712Field, len(typed.Types))
	for name, fields := range typed.Types {
		converted := make([]erc8004.EIP712Field, len(fields))
		for i, f := range fields {
			converted[i] = erc8004.EIP712Field{Name: f.Name, Type: f.Type}
		}
		remoteTypes[name] = converted
	}
	remote := erc8004.EIP712TypedData{
		Types:       remoteTypes,
		PrimaryType: "PermitBatchTransferFrom",
		Domain: map[string]interface{}{
			"name":              "Permit2",
			"chainId":           chainID.String(),
			"verifyingContract": Permit2Address,
		},
		Message: message,
	}
	return typed, remote, nil
}

// HashVoucher returns the canonical EIP-712 digest the voucher signature
// commits to.
func HashVoucher(v Permit2Voucher, chainID *big.Int) (common.Hash, error) {
	typed, _, err := VoucherTypedData(v, chainID)
	if err != nil {
		return common.Hash{}, err
	}
	digest, _, err := apitypes.TypedDataAndHash(typed)
	if err != nil {
		return common.Hash{}, fmt.Errorf("permit2: hash typed data: %w", err)
	}
	return common.BytesToHash(digest), nil
}

// SignVoucher signs the voucher with key and fills v.Signature (65-byte
// 0x-hex, v in Ethereum 27/28 convention). When v.Owner is empty it is filled
// from the key; when set it must match the key's address.
func SignVoucher(v *Permit2Voucher, chainID *big.Int, key *ecdsa.PrivateKey) error {
	if v == nil {
		return fmt.Errorf("permit2: nil voucher")
	}
	if key == nil {
		return fmt.Errorf("permit2: nil signing key")
	}
	addr := crypto.PubkeyToAddress(key.PublicKey)
	if v.Owner == "" {
		v.Owner = addr.Hex()
	} else if common.HexToAddress(v.Owner) != addr {
		return fmt.Errorf("permit2: voucher owner %s does not match signing key %s", v.Owner, addr.Hex())
	}

	hash, err := HashVoucher(*v, chainID)
	if err != nil {
		return err
	}
	sig, err := crypto.Sign(hash.Bytes(), key)
	if err != nil {
		return fmt.Errorf("permit2: sign voucher: %w", err)
	}
	sig[64] += 27
	v.Signature = hexutil.Encode(sig)
	return nil
}

// VerifyVoucher checks that the voucher is internally valid (addresses parse,
// every amount is a positive integer), names expectedSpender as the only
// executor, has not expired, and carries a signature that recovers to Owner.
func VerifyVoucher(v Permit2Voucher, chainID *big.Int, expectedSpender common.Address) error {
	if err := validateVoucherFields(v); err != nil {
		return err
	}
	if expectedSpender == (common.Address{}) {
		return fmt.Errorf("permit2: expected spender is unset")
	}
	if common.HexToAddress(v.Spender) != expectedSpender {
		return fmt.Errorf("permit2: voucher spender %s is not the facilitator %s", v.Spender, expectedSpender.Hex())
	}
	if v.Deadline <= time.Now().Unix() {
		return fmt.Errorf("permit2: voucher expired at %d", v.Deadline)
	}

	sig, err := hexutil.Decode(v.Signature)
	if err != nil {
		return fmt.Errorf("permit2: decode signature: %w", err)
	}
	if len(sig) != 65 {
		return fmt.Errorf("permit2: signature must be 65 bytes, got %d", len(sig))
	}
	// Normalize Ethereum 27/28 recovery id to 0/1 for SigToPub.
	recoverable := make([]byte, 65)
	copy(recoverable, sig)
	if recoverable[64] >= 27 {
		recoverable[64] -= 27
	}

	hash, err := HashVoucher(v, chainID)
	if err != nil {
		return err
	}
	pub, err := crypto.SigToPub(hash.Bytes(), recoverable)
	if err != nil {
		return fmt.Errorf("permit2: recover signer: %w", err)
	}
	recovered := crypto.PubkeyToAddress(*pub)
	if recovered != common.HexToAddress(v.Owner) {
		return fmt.Errorf("permit2: signature recovers to %s, voucher owner is %s", recovered.Hex(), v.Owner)
	}
	return nil
}
