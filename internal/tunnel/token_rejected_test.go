package tunnel

import "testing"

func TestTokenRejectedInLogs(t *testing.T) {
	rejected := `2026-10-08T09:00:00Z INF Starting tunnel tunnelID=abc
2026-10-08T09:00:01Z ERR Register tunnel error from server side error="Unauthorized: Tunnel not found" connIndex=0`
	if !tokenRejectedInLogs(rejected) {
		t.Fatal("did not detect a rejected connector token")
	}

	healthy := `2026-10-08T09:00:01Z INF Registered tunnel connection connIndex=0
2026-10-08T09:00:02Z ERR failed to serve request error="Unauthorized"`
	if tokenRejectedInLogs(healthy) {
		t.Fatal("flagged an unrelated Unauthorized line as a rejected token")
	}
}
