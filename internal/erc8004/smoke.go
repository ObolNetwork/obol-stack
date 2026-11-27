// Smoke-test ↔ ERC-8004 grounding: the smoke-test request hash binds an
// operator's on-chain validationResponse to one specific (target, run) pair,
// so a published smoke report (committed to a public GitHub repo) can be
// checked against a chain-anchored verdict entry.

package erc8004

import (
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// smokeTestDomain is the versioned domain prefix for smoke-test request
// hashes. Changing it is a breaking change for every published verdict.
const smokeTestDomain = "obol/smoke-test/v1"

// normalizeSmokeTarget canonicalizes the probed base URL exactly the way the
// in-pod smoke-test skill does (python `.strip().rstrip("/")`): surrounding
// whitespace and trailing slashes never change the request hash.
func normalizeSmokeTarget(u string) string {
	return strings.TrimRight(strings.TrimSpace(u), "/")
}

// SmokeTestRequestHash derives the ERC-8004 validation request hash for one
// smoke-test run: keccak256 of the exact ASCII bytes
// "obol/smoke-test/v1|<normalized targetBaseURL>|<runId>". The CLI
// (`obol smoke calldata`, operator side) and any grounding consumer MUST
// compute this identically. The in-pod skill never computes it — there is no
// reliable keccak256 in the pod's python stdlib (hashlib.sha3_256 is NIST
// SHA-3, not keccak256) — it only echoes the normalized target into
// results.json.
func SmokeTestRequestHash(targetBaseURL, runID string) common.Hash {
	return crypto.Keccak256Hash([]byte(smokeTestDomain + "|" + normalizeSmokeTarget(targetBaseURL) + "|" + strings.TrimSpace(runID)))
}
