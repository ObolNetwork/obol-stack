package testutil

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestBenchmark_OBOLSessionPermitEscrow(t *testing.T) {
	anvil := StartAnvilFork(t)

	facilitator := anvil.Accounts[0]
	seller := anvil.Accounts[1]
	buyer := anvil.Accounts[2]
	for _, addr := range []string{facilitator.Address, seller.Address, buyer.Address} {
		anvil.ClearCode(t, addr)
	}

	obolToken := anvil.DeployForkObolToken(t, facilitator.PrivateKey, facilitator.Address, big.NewInt(0))
	escrow := anvil.DeploySessionPermitEscrow(t, facilitator.PrivateKey, facilitator.Address)

	pricePerRequest := big.NewInt(1_000_000_000_000_000) // 0.001 OBOL
	requestCount := int64(3)
	authorizedAmount := new(big.Int).Mul(pricePerRequest, big.NewInt(requestCount))

	anvil.FundETH(t, buyer.Address, big.NewInt(1e18))
	anvil.MintMintableERC20(t, obolToken, facilitator.PrivateKey, buyer.Address, new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)))

	deadline := big.NewInt(time.Now().Add(5 * time.Minute).Unix())
	permitSig := SignERC20PermitSignature(
		t,
		buyer.PrivateKey,
		obolToken,
		"Obol Network",
		"1",
		escrow,
		authorizedAmount,
		big.NewInt(0),
		deadline,
		84532,
	)

	salt := crypto.Keccak256Hash([]byte("obol-session-benchmark"))
	sessionID := sessionIDFor(t, buyer.Address, seller.Address, obolToken, authorizedAmount, salt)

	buyerBefore := anvil.GetERC20Balance(t, obolToken, buyer.Address)
	sellerBefore := anvil.GetERC20Balance(t, obolToken, seller.Address)

	authorizeReceipt := anvil.SendContractTx(
		t,
		facilitator.PrivateKey,
		escrow,
		"authorizeWithPermitAndDeposit(address,address,address,uint256,uint256,bytes,bytes32)",
		obolToken,
		buyer.Address,
		seller.Address,
		authorizedAmount.String(),
		deadline.String(),
		permitSig,
		salt.Hex(),
	)

	closeReceipt := anvil.SendContractTx(
		t,
		facilitator.PrivateKey,
		escrow,
		"close(bytes32,uint256)",
		sessionID.Hex(),
		authorizedAmount.String(),
	)

	buyerAfter := anvil.GetERC20Balance(t, obolToken, buyer.Address)
	sellerAfter := anvil.GetERC20Balance(t, obolToken, seller.Address)

	buyerDelta := new(big.Int).Sub(buyerBefore, buyerAfter)
	sellerDelta := new(big.Int).Sub(sellerAfter, sellerBefore)
	if buyerDelta.Cmp(authorizedAmount) != 0 {
		t.Fatalf("buyer delta = %s, want %s", buyerDelta, authorizedAmount)
	}
	if sellerDelta.Cmp(authorizedAmount) != 0 {
		t.Fatalf("seller delta = %s, want %s", sellerDelta, authorizedAmount)
	}

	authorizeGasUsed := ParseHexBigInt(t, authorizeReceipt.GasUsed)
	authorizeGasPrice := ParseHexBigInt(t, authorizeReceipt.EffectiveGasPrice)
	closeGasUsed := ParseHexBigInt(t, closeReceipt.GasUsed)
	closeGasPrice := ParseHexBigInt(t, closeReceipt.EffectiveGasPrice)
	authorizeGasWei := new(big.Int).Mul(new(big.Int).Set(authorizeGasUsed), authorizeGasPrice)
	closeGasWei := new(big.Int).Mul(new(big.Int).Set(closeGasUsed), closeGasPrice)
	totalGasUsed := new(big.Int).Add(new(big.Int).Set(authorizeGasUsed), closeGasUsed)
	totalGasWei := new(big.Int).Add(new(big.Int).Set(authorizeGasWei), closeGasWei)

	t.Logf(
		"OBOL session benchmark authorize: tx=%s gasUsed=%s gasWei=%s",
		authorizeReceipt.TransactionHash,
		authorizeGasUsed.String(),
		authorizeGasWei.String(),
	)
	t.Logf(
		"OBOL session benchmark close: tx=%s gasUsed=%s gasWei=%s",
		closeReceipt.TransactionHash,
		closeGasUsed.String(),
		closeGasWei.String(),
	)
	t.Logf(
		"OBOL session benchmark summary: requests=%d totalGasUsed=%s totalGasWei=%s avgGasUsedPerRequest=%s avgGasWeiPerRequest=%s",
		requestCount,
		totalGasUsed.String(),
		totalGasWei.String(),
		new(big.Int).Div(new(big.Int).Set(totalGasUsed), big.NewInt(requestCount)).String(),
		new(big.Int).Div(new(big.Int).Set(totalGasWei), big.NewInt(requestCount)).String(),
	)
}

func sessionIDFor(
	t *testing.T,
	owner string,
	seller string,
	token string,
	amount *big.Int,
	salt common.Hash,
) common.Hash {
	t.Helper()

	addressTy, err := abi.NewType("address", "", nil)
	if err != nil {
		t.Fatalf("abi address type: %v", err)
	}
	uintTy, err := abi.NewType("uint256", "", nil)
	if err != nil {
		t.Fatalf("abi uint256 type: %v", err)
	}
	bytes32Ty, err := abi.NewType("bytes32", "", nil)
	if err != nil {
		t.Fatalf("abi bytes32 type: %v", err)
	}

	args := abi.Arguments{
		{Type: addressTy},
		{Type: addressTy},
		{Type: addressTy},
		{Type: uintTy},
		{Type: bytes32Ty},
	}
	encoded, err := args.Pack(
		common.HexToAddress(owner),
		common.HexToAddress(seller),
		common.HexToAddress(token),
		amount,
		salt,
	)
	if err != nil {
		t.Fatalf("abi encode session id: %v", err)
	}

	return crypto.Keccak256Hash(encoded)
}
