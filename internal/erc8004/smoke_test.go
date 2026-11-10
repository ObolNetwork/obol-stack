package erc8004

import "testing"

// TestSmokeTestRequestHash_Golden pins the exact preimage layout
// ("obol/smoke-test/v1|<normalized targetBaseURL>|<runId>"). The operator
// submits validationResponses against this hash via `obol smoke calldata` and
// grounding consumers match it on-chain — any drift silently breaks
// grounding, so the vector is hardcoded, not recomputed.
func TestSmokeTestRequestHash_Golden(t *testing.T) {
	const (
		target = "http://obol.stack:8080"
		runID  = "20260101T000000Z-ab12cd"
		golden = "0x2a28aa12a52a28414de4933bbe8d1e52e42828ba08006748f544596823ce7a57"
	)

	if got := SmokeTestRequestHash(target, runID).Hex(); got != golden {
		t.Errorf("SmokeTestRequestHash = %s, want %s", got, golden)
	}

	// The target is normalized exactly like the in-pod skill's
	// `.strip().rstrip("/")`: trailing-slash and surrounding-whitespace
	// variants of the same target MUST hash identically, and a padded runId
	// is trimmed.
	variants := []struct {
		name, target, runID string
	}{
		{"trailing slash", target + "/", runID},
		{"double trailing slash", target + "//", runID},
		{"surrounding whitespace", "  " + target + " \n", runID},
		{"whitespace + slash", " " + target + "/ ", runID},
		{"padded runId", target, " " + runID + "\t"},
	}
	for _, v := range variants {
		if got := SmokeTestRequestHash(v.target, v.runID).Hex(); got != golden {
			t.Errorf("%s: hash = %s, want %s (normalization must be hash-invariant)", v.name, got, golden)
		}
	}

	// Different target or runId must never collide with the golden pair.
	if SmokeTestRequestHash("http://other.example:8080", runID).Hex() == golden {
		t.Error("different target produced the golden hash")
	}
	if SmokeTestRequestHash(target, "20260101T000000Z-ffffff").Hex() == golden {
		t.Error("different runId produced the golden hash")
	}
}
