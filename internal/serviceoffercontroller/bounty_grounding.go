package serviceoffercontroller

// Grounding: an annotation-level reveal that carries a validationTx claims an
// on-chain ERC-8004 validationResponse backs it. The controller READS the
// Validation Registry on the bounty's payment network (per-network client via
// eRPC, ERC8004_RPC_BASE — the registration watcher pattern) and marks the
// evaluation Grounded only when the on-chain responder is the evaluator AND
// the on-chain response equals the revealed score, for the request hash
// erc8004.BountyEvalRequestHash(bountyUID, evaluator).
//
// Grounding is ADVISORY reputation signal: chain unreachable, no entry, or a
// mismatch leaves Grounded=false with a condition explaining why — it never
// blocks, delays, or changes the quorum verdict. The controller still signs
// nothing; the validationResponse tx was submitted by the evaluator's own
// wallet.

import (
	"context"
	"fmt"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/monetizeapi"
	"github.com/ethereum/go-ethereum/common"
)

// bountyValidationReader is the narrow chain-read seam grounding needs.
type bountyValidationReader interface {
	ValidationStatus(ctx context.Context, requestHash common.Hash) (erc8004.ValidationStatus, error)
}

// bountyValidationReaderFactory dials a read-only ERC-8004 Validation Registry
// reader for the given network. It is a package seam (the grounding twin of
// the bountyEscrow fake) swapped by tests to inject a fake chain; it cannot be
// a Controller field without editing controller.go, which a parallel lane
// owns. The returned func() releases the underlying RPC client.
var bountyValidationReaderFactory = func(ctx context.Context, rpcBase, network string) (bountyValidationReader, func(), error) {
	net, err := erc8004.ResolveNetwork(network)
	if err != nil {
		return nil, nil, err
	}
	registry, err := erc8004.ValidationRegistryAddress(network)
	if err != nil {
		return nil, nil, err
	}
	client, err := erc8004.NewClientForNetwork(ctx, rpcBase, net)
	if err != nil {
		return nil, nil, err
	}
	reader, err := erc8004.NewValidationReader(client.ETH(), registry)
	if err != nil {
		client.Close()
		return nil, nil, err
	}
	return reader, client.Close, nil
}

// groundEvaluations sets Grounded on every revealed evaluation in the slice
// whose validationTx claim is backed by a matching on-chain validation entry.
// It runs BEFORE ladder bookkeeping (recordLadder reads Grounded) and mutates
// only the Grounded flags + the EvalGrounded condition — never the verdict.
func (c *Controller) groundEvaluations(ctx context.Context, sb *monetizeapi.ServiceBounty, status *monetizeapi.ServiceBountyStatus, evaluations []monetizeapi.ServiceBountyEvaluation) {
	var pending []int
	for i := range evaluations {
		if evaluations[i].Phase == evalPhaseRevealed &&
			strings.TrimSpace(evaluations[i].ValidationTxHash) != "" &&
			!evaluations[i].Grounded {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 {
		return // nothing claims chain backing — touch no condition, dial nothing
	}

	network := sb.Spec.Reward.Network
	if _, err := erc8004.ValidationRegistryAddress(network); err != nil {
		setPurchaseCondition(&status.Conditions, "EvalGrounded", "False", "RegistryUnavailable",
			truncateMessage(fmt.Sprintf("no validation registry for network %q: %v", network, err)))
		return
	}

	rpcBase := c.registrationRPCBase
	if rpcBase == "" {
		rpcBase = erc8004.DefaultRPCBase
	}
	reader, closeReader, err := bountyValidationReaderFactory(ctx, rpcBase, network)
	if err != nil {
		setPurchaseCondition(&status.Conditions, "EvalGrounded", "False", "ChainUnreachable",
			truncateMessage(fmt.Sprintf("validation registry on %s unreachable: %v", network, err)))
		return
	}
	defer closeReader()

	grounded := 0
	var problems []string
	for _, i := range pending {
		evaluation := &evaluations[i]
		requestHash := erc8004.BountyEvalRequestHash(string(sb.UID), evaluation.Address)
		onchain, err := reader.ValidationStatus(ctx, requestHash)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: chain read failed: %v", evaluation.Address, err))
		case onchain.ValidatorAddress == (common.Address{}):
			problems = append(problems, fmt.Sprintf("%s: no on-chain validation entry", evaluation.Address))
		case onchain.ValidatorAddress != common.HexToAddress(evaluation.Address):
			problems = append(problems, fmt.Sprintf("%s: on-chain responder %s is not the evaluator", evaluation.Address, onchain.ValidatorAddress.Hex()))
		case int64(onchain.Response) != evaluation.Score:
			problems = append(problems, fmt.Sprintf("%s: on-chain response %d != revealed score %d", evaluation.Address, onchain.Response, evaluation.Score))
		default:
			evaluation.Grounded = true
			grounded++
		}
	}

	if len(problems) == 0 {
		setPurchaseCondition(&status.Conditions, "EvalGrounded", "True", "Grounded",
			fmt.Sprintf("%d evaluation(s) grounded by on-chain ERC-8004 validation entries", grounded))
	} else {
		setPurchaseCondition(&status.Conditions, "EvalGrounded", "False", "NotGrounded",
			truncateMessage(strings.Join(problems, "; ")))
	}
}
