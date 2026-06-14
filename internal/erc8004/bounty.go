// ServiceBounty ↔ ERC-8004 grounding: the eval-request hash binds an
// evaluator's on-chain validationResponse to one specific bounty + evaluator
// pair, so an annotation-level reveal can be checked against a chain-anchored
// entry (a "grounded" verdict).

package erc8004

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// bountyEvalDomain is the versioned domain prefix for bounty eval-request
// hashes. Changing it is a breaking change for every grounded verdict.
const bountyEvalDomain = "obol/bounty-eval/v1"

// BountyEvalRequestHash derives the ERC-8004 validation request hash for one
// (bounty, evaluator) pair: keccak256 of the exact ASCII bytes
// "obol/bounty-eval/v1|<bountyUID>|<lowercase evaluator>". The CLI (evaluator
// side, submitting validationResponse) and the controller (grounding side,
// matching chain entries) MUST compute this identically.
func BountyEvalRequestHash(bountyUID, evaluator string) common.Hash {
	return crypto.Keccak256Hash([]byte(bountyEvalDomain + "|" + bountyUID + "|" + strings.ToLower(evaluator)))
}
