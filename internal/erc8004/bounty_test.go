package erc8004

import "testing"

// TestBountyEvalRequestHash_Golden pins the exact preimage layout
// ("obol/bounty-eval/v1|<uid>|<lowercase evaluator>"). The CLI signs
// validationResponses against this hash and the controller grounds verdicts
// by matching it on-chain — any drift silently breaks grounding, so the
// vector is hardcoded, not recomputed.
func TestBountyEvalRequestHash_Golden(t *testing.T) {
	const (
		bountyUID = "8b9af0d4-9c3e-4a64-b1d0-2f50f2a1c111"
		evaluator = "0xAbCdEf0123456789aBcDeF0123456789AbCdEf01"
		golden    = "0x22683f2360f35f41b5e5122865e048bb0dcb3b7896fc7280545fb09fbfdfa51a"
	)

	if got := BountyEvalRequestHash(bountyUID, evaluator).Hex(); got != golden {
		t.Errorf("BountyEvalRequestHash = %s, want %s", got, golden)
	}

	// The evaluator address is lowercased into the preimage: checksummed and
	// lowercase forms of the same address must ground identically.
	lower := BountyEvalRequestHash(bountyUID, "0xabcdef0123456789abcdef0123456789abcdef01")
	if lower.Hex() != golden {
		t.Errorf("lowercase evaluator hash = %s, want %s (address must be case-insensitive)", lower.Hex(), golden)
	}

	// Different bounty or evaluator must never collide with the golden pair.
	if BountyEvalRequestHash("other-uid", evaluator).Hex() == golden {
		t.Error("different bountyUID produced the golden hash")
	}
	if BountyEvalRequestHash(bountyUID, "0x0000000000000000000000000000000000000001").Hex() == golden {
		t.Error("different evaluator produced the golden hash")
	}
}
