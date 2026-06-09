package buy

import (
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/agentruntime"
)

func TestBuyPyCommandRuntimePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		runtime agentruntime.Runtime
		want    []string
	}{
		{
			name:    "hermes",
			runtime: agentruntime.Hermes,
			want: []string{
				"/opt/hermes/.venv/bin/python3",
				"/data/.hermes/obol-skills/buy-x402/scripts/buy.py",
				"list",
				"--json",
			},
		},
		{
			name:    "openclaw",
			runtime: agentruntime.OpenClaw,
			want: []string{
				"python3",
				"/data/.openclaw/skills/buy-x402/scripts/buy.py",
				"list",
				"--json",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuyPyCommand(tc.runtime, "list", "--json")
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuyPyCommand() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
