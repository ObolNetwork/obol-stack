package doctor

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCheckEthereumRestarts(t *testing.T) {
	recent := time.Now().Add(-12 * time.Minute).UTC().Format(time.RFC3339)
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)

	pod := func(ns, name, container string, restarts int, finished string) string {
		last := `{}`
		if finished != "" {
			last = `{"terminated":{"reason":"Completed","exitCode":0,"finishedAt":"` + finished + `"}}`
		}

		return `{"metadata":{"name":"` + name + `","namespace":"` + ns + `"},"status":{"containerStatuses":[{"name":"` + container +
			`","restartCount":` + strconv.Itoa(restarts) + `,"lastState":` + last + `}]}}`
	}

	list := func(pods ...string) string { return `{"items":[` + strings.Join(pods, ",") + `]}` }

	cases := []struct {
		name    string
		pods    string
		status  Status
		message string
		fix     string
	}{
		{
			name:    "silver restarting",
			pods:    list(pod("ethereum-hoodi", "ethereum-beacon-0", "silver", 20, recent), pod("ethereum-hoodi", "ethereum-execution-0", "reth", 0, "")),
			status:  StatusWarn,
			message: "ethereum-hoodi/ethereum-beacon-0 silver: 20 restarts, last Completed (exit 0) 12m ago",
			fix:     "obol kubectl -n ethereum-hoodi logs --previous ethereum-beacon-0 -c silver",
		},
		{name: "restarts long ago", pods: list(pod("ethereum-hoodi", "ethereum-beacon-0", "silver", 20, old)), status: StatusOK},
		{name: "few restarts", pods: list(pod("ethereum-hoodi", "ethereum-beacon-0", "silver", 2, recent)), status: StatusOK},
		{name: "other namespaces", pods: list(pod("llm", "litellm-0", "litellm", 9, recent)), status: StatusSkip},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := fakeEnv(t, map[string]string{"kubectl --kubeconfig": c.pods})
			r := checkEthereumRestarts(context.Background(), e)

			if r.Status != c.status || !strings.Contains(r.Message, c.message) || !strings.Contains(r.Fix, c.fix) {
				t.Fatalf("got %s %q fix %q, want %s %q fix %q", r.Status, r.Message, r.Fix, c.status, c.message, c.fix)
			}
		})
	}
}
