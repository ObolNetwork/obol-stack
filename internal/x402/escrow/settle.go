package escrow

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/x402"
)

// permit2TransferABI is the minimal ABI fragment for Permit2
// SignatureTransfer's batch permitTransferFrom. Note the calldata permit tuple
// carries NO spender — Permit2 enforces spender == msg.sender on-chain, which
// is exactly why the facilitator address must be signed into the voucher.
const permit2TransferABI = `[{
  "name": "permitTransferFrom",
  "type": "function",
  "stateMutability": "nonpayable",
  "inputs": [
    {"name": "permit", "type": "tuple", "components": [
      {"name": "permitted", "type": "tuple[]", "components": [
        {"name": "token", "type": "address"},
        {"name": "amount", "type": "uint256"}
      ]},
      {"name": "nonce", "type": "uint256"},
      {"name": "deadline", "type": "uint256"}
    ]},
    {"name": "transferDetails", "type": "tuple[]", "components": [
      {"name": "to", "type": "address"},
      {"name": "requestedAmount", "type": "uint256"}
    ]},
    {"name": "owner", "type": "address"},
    {"name": "signature", "type": "bytes"}
  ],
  "outputs": []
}]`

var permit2ABI = sync.OnceValues(func() (abi.ABI, error) {
	return abi.JSON(strings.NewReader(permit2TransferABI))
})

// permit2TokenPermissions mirrors Permit2's TokenPermissions struct for ABI
// packing (component names token/amount map to these fields).
type permit2TokenPermissions struct {
	Token  common.Address
	Amount *big.Int
}

// permit2PermitBatchTransferFrom mirrors ISignatureTransfer.PermitBatchTransferFrom.
type permit2PermitBatchTransferFrom struct {
	Permitted []permit2TokenPermissions
	Nonce     *big.Int
	Deadline  *big.Int
}

// permit2SignatureTransferDetails mirrors ISignatureTransfer.SignatureTransferDetails.
type permit2SignatureTransferDetails struct {
	To              common.Address
	RequestedAmount *big.Int
}

// TransferDetail is one entry of the on-chain transferDetails array. It pairs
// INDEX-WISE with the voucher's Recipients/permitted array: omitted seats stay
// in the array with Amount 0 (Permit2 allows requesting less than permitted,
// including zero) — the array is never shortened.
type TransferDetail struct {
	To     common.Address
	Amount *big.Int
}

// HumanToAtomic converts a human-unit decimal amount (e.g. "500.00") to
// atomic token units ("500000000" at 6 decimals) without float rounding.
// Trailing zeros beyond the token's precision are tolerated; non-zero
// sub-atomic remainders are an error. Every voucher seat amount and every
// capture recipient amount MUST be atomic — BuildTransferDetails matches
// requested recipients against signed seats with exact integer comparison.
func HumanToAtomic(amount string, decimals int) (string, error) {
	s := strings.TrimSpace(amount)
	if s == "" {
		return "", fmt.Errorf("amount is empty")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("amount %q is not a non-negative decimal number", amount)
		}
	}
	if len(frac) > decimals {
		if strings.Trim(frac[decimals:], "0") != "" {
			return "", fmt.Errorf("amount %q has more than %d decimal places", amount, decimals)
		}
		frac = frac[:decimals]
	}
	frac += strings.Repeat("0", decimals-len(frac))
	v, ok := new(big.Int).SetString(whole+frac, 10)
	if !ok {
		return "", fmt.Errorf("amount %q is not a decimal number", amount)
	}
	if v.Sign() <= 0 {
		return "", fmt.Errorf("amount %q must be positive", amount)
	}
	return v.String(), nil
}

// BuildTransferDetails validates that requested is a subset of the voucher's
// recipient seats with exactly matching per-seat amounts, and returns the
// index-wise transferDetails array: matched seats carry their signed amount,
// omitted seats carry zero (unpaid). Duplicate seats are consumed one
// requested entry per seat.
func BuildTransferDetails(v Permit2Voucher, requested []BatchRecipient) ([]TransferDetail, error) {
	if len(v.Recipients) == 0 {
		return nil, fmt.Errorf("escrow settle: voucher has no recipients")
	}
	if len(requested) == 0 {
		return nil, fmt.Errorf("escrow settle: no recipients requested")
	}

	consumed := make([]bool, len(v.Recipients))
	amounts := make([]*big.Int, len(v.Recipients))
	for _, r := range requested {
		want, err := parsePositiveAmount("requested amount", r.Amount)
		if err != nil {
			return nil, err
		}
		matched := false
		for i, seat := range v.Recipients {
			if consumed[i] || !strings.EqualFold(seat.Address, r.Address) {
				continue
			}
			signed, err := parsePositiveAmount("voucher amount", seat.Amount)
			if err != nil {
				return nil, err
			}
			if signed.Cmp(want) != 0 {
				continue // amount mismatch on this seat; another seat may match
			}
			consumed[i] = true
			amounts[i] = want
			matched = true
			break
		}
		if !matched {
			return nil, fmt.Errorf("escrow settle: requested recipient %s amount %s does not match any unconsumed voucher seat", r.Address, r.Amount)
		}
	}

	details := make([]TransferDetail, len(v.Recipients))
	for i, seat := range v.Recipients {
		amount := amounts[i]
		if amount == nil {
			amount = new(big.Int) // omitted seat: requestedAmount = 0
		}
		details[i] = TransferDetail{To: common.HexToAddress(seat.Address), Amount: amount}
	}
	return details, nil
}

// BuildPermitTransferFromCalldata packs the permitTransferFrom calldata for a
// signed voucher and the index-wise transferDetails from BuildTransferDetails.
// len(details) must equal len(v.Recipients) — transferDetails[i] pairs with
// permitted[i].
func BuildPermitTransferFromCalldata(v Permit2Voucher, details []TransferDetail) ([]byte, error) {
	if err := validateVoucherFields(v); err != nil {
		return nil, err
	}
	if len(details) != len(v.Recipients) {
		return nil, fmt.Errorf("escrow settle: transferDetails length %d must equal voucher recipients %d (omitted seats get amount 0, the array is never shortened)", len(details), len(v.Recipients))
	}

	token := common.HexToAddress(v.Token)
	permitted := make([]permit2TokenPermissions, len(v.Recipients))
	for i, seat := range v.Recipients {
		amount, err := parsePositiveAmount("voucher amount", seat.Amount)
		if err != nil {
			return nil, err
		}
		permitted[i] = permit2TokenPermissions{Token: token, Amount: amount}
	}
	nonce, err := parseUint256("nonce", v.Nonce)
	if err != nil {
		return nil, err
	}
	permit := permit2PermitBatchTransferFrom{
		Permitted: permitted,
		Nonce:     nonce,
		Deadline:  big.NewInt(v.Deadline),
	}

	transferDetails := make([]permit2SignatureTransferDetails, len(details))
	for i, d := range details {
		amount := d.Amount
		if amount == nil {
			amount = new(big.Int)
		}
		if amount.Sign() < 0 {
			return nil, fmt.Errorf("escrow settle: transferDetails[%d] amount is negative", i)
		}
		transferDetails[i] = permit2SignatureTransferDetails{To: d.To, RequestedAmount: amount}
	}

	sig, err := hexutil.Decode(v.Signature)
	if err != nil {
		return nil, fmt.Errorf("escrow settle: decode voucher signature: %w", err)
	}

	parsed, err := permit2ABI()
	if err != nil {
		return nil, fmt.Errorf("escrow settle: parse permit2 abi: %w", err)
	}
	calldata, err := parsed.Pack("permitTransferFrom", permit, transferDetails, common.HexToAddress(v.Owner), sig)
	if err != nil {
		return nil, fmt.Errorf("escrow settle: pack permitTransferFrom: %w", err)
	}
	return calldata, nil
}

// Submitter executes a verified voucher on-chain. Abstracted so the server
// and its tests never need a live chain.
type Submitter interface {
	// Submit sends permitTransferFrom(voucher, details) and waits for the
	// receipt. details must pair index-wise with v.Recipients.
	Submit(ctx context.Context, v Permit2Voucher, details []TransferDetail) (txHash string, err error)
}

// erpcNetworkSuffix maps a voucher network alias to the eRPC path segment,
// following the same pattern as erc8004.NewClientForNetwork
// (<base>/<ERPCNetwork>, e.g. "ethereum" → "mainnet").
func erpcNetworkSuffix(network string) string {
	if net, err := erc8004.ResolveNetwork(network); err == nil {
		return net.ERPCNetwork
	}
	if info, err := x402.ResolveChainInfo(network); err == nil {
		return info.Name
	}
	return strings.TrimSpace(network)
}

// EthSubmitter submits permitTransferFrom transactions via JSON-RPC, signing
// locally with Key or remotely via Signer (exactly one should be set).
type EthSubmitter struct {
	// RPCBase is the per-network JSON-RPC base; the endpoint is
	// <RPCBase>/<network-suffix>. Defaults to erc8004.DefaultRPCBase
	// (in-cluster eRPC).
	RPCBase string
	// Key signs locally when set.
	Key *ecdsa.PrivateKey
	// Signer signs via the remote-signer REST API when Key is nil.
	Signer *erc8004.RemoteSigner
	// SignerAddress is the remote signer's address; resolved via
	// Signer.GetAddress when zero.
	SignerAddress common.Address
	// ReceiptTimeout bounds the wait for the settlement receipt (default 2m).
	ReceiptTimeout time.Duration
}

func (s *EthSubmitter) Submit(ctx context.Context, v Permit2Voucher, details []TransferDetail) (string, error) {
	if s.Key == nil && s.Signer == nil {
		return "", fmt.Errorf("escrow settle: no signing key configured (set OBOL_ESCROW_KEY or OBOL_ESCROW_SIGNER_URL)")
	}

	base := s.RPCBase
	if base == "" {
		base = erc8004.DefaultRPCBase
	}
	rpcURL := strings.TrimRight(base, "/") + "/" + erpcNetworkSuffix(v.Network)
	eth, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return "", fmt.Errorf("escrow settle: dial %s: %w", rpcURL, err)
	}
	defer eth.Close()

	chainID, err := eth.ChainID(ctx)
	if err != nil {
		return "", fmt.Errorf("escrow settle: chain id: %w", err)
	}
	if want, resolveErr := ChainIDForNetwork(v.Network); resolveErr == nil && want.Cmp(chainID) != 0 {
		return "", fmt.Errorf("escrow settle: rpc %s reports chain %s but voucher network %q expects %s", rpcURL, chainID, v.Network, want)
	}

	calldata, err := BuildPermitTransferFromCalldata(v, details)
	if err != nil {
		return "", err
	}

	var opts *bind.TransactOpts
	if s.Key != nil {
		opts, err = bind.NewKeyedTransactorWithChainID(s.Key, chainID)
		if err != nil {
			return "", fmt.Errorf("escrow settle: transactor: %w", err)
		}
		opts.Context = ctx
	} else {
		addr := s.SignerAddress
		if addr == (common.Address{}) {
			addr, err = s.Signer.GetAddress(ctx)
			if err != nil {
				return "", fmt.Errorf("escrow settle: resolve remote signer address: %w", err)
			}
		}
		opts = s.Signer.RemoteTransactOpts(ctx, addr, chainID)
	}

	parsed, err := permit2ABI()
	if err != nil {
		return "", fmt.Errorf("escrow settle: parse permit2 abi: %w", err)
	}
	contract := bind.NewBoundContract(common.HexToAddress(Permit2Address), parsed, eth, eth, eth)
	tx, err := contract.RawTransact(opts, calldata)
	if err != nil {
		return "", fmt.Errorf("escrow settle: submit permitTransferFrom: %w", err)
	}

	timeout := s.ReceiptTimeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	receipt, err := bind.WaitMined(waitCtx, eth, tx)
	if err != nil {
		return "", fmt.Errorf("escrow settle: wait receipt for %s: %w", tx.Hash().Hex(), err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return "", fmt.Errorf("escrow settle: permitTransferFrom %s reverted", tx.Hash().Hex())
	}
	return tx.Hash().Hex(), nil
}
