package agentruntime

import (
	"reflect"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestBuildExecArgs(t *testing.T) {
	tests := []struct {
		name    string
		runtime Runtime
		id      string
		argv    []string
		withTTY bool
		want    []string
	}{
		{
			name:    "hermes default instance, no TTY",
			runtime: Hermes,
			id:      DefaultInstanceID,
			argv:    []string{"/opt/hermes/.venv/bin/hermes", "skills", "audit"},
			withTTY: false,
			want: []string{
				"exec", "-i",
				"-c", "hermes",
				"-n", "hermes-obol-agent",
				"deploy/hermes",
				"--",
				"/opt/hermes/.venv/bin/hermes", "skills", "audit",
			},
		},
		{
			name:    "hermes default instance, with TTY",
			runtime: Hermes,
			id:      DefaultInstanceID,
			argv:    []string{"/opt/hermes/.venv/bin/python3", "/data/.hermes/obol-skills/buy-x402/scripts/buy.py", "buy", "demo", "--budget", "1000000"},
			withTTY: true,
			want: []string{
				"exec", "-i", "-t",
				"-c", "hermes",
				"-n", "hermes-obol-agent",
				"deploy/hermes",
				"--",
				"/opt/hermes/.venv/bin/python3",
				"/data/.hermes/obol-skills/buy-x402/scripts/buy.py",
				"buy", "demo",
				"--budget", "1000000",
			},
		},
		{
			name:    "openclaw alternate runtime, no TTY",
			runtime: OpenClaw,
			id:      "research",
			argv:    []string{"node", "openclaw.mjs", "skills", "list"},
			withTTY: false,
			want: []string{
				"exec", "-i",
				"-c", "openclaw",
				"-n", "openclaw-research",
				"deploy/openclaw",
				"--",
				"node", "openclaw.mjs", "skills", "list",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := BuildExecArgs(tc.runtime, tc.id, tc.argv, tc.withTTY)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildExecArgs() = %#v\n want                 %#v", got, tc.want)
			}
		})
	}
}

func TestExecInPod_EmptyArgvRejected(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir(), BinDir: t.TempDir()}
	err := ExecInPod(cfg, Hermes, DefaultInstanceID, nil)
	if err == nil || err.Error() != "ExecInPod: argv is empty" {
		t.Fatalf("ExecInPod(nil argv) err = %v, want \"ExecInPod: argv is empty\"", err)
	}
}

func TestExecInPod_ClusterDownErrorMessage(t *testing.T) {
	// Empty ConfigDir means kubeconfig.yaml does not exist → human-readable error,
	// not a kubectl crash. Lets `obol buy inference` produce a usable message
	// when the cluster isn't running.
	cfg := &config.Config{ConfigDir: t.TempDir(), BinDir: t.TempDir()}
	err := ExecInPod(cfg, Hermes, DefaultInstanceID, []string{"true"})
	if err == nil {
		t.Fatal("expected error when kubeconfig missing, got nil")
	}
	if got := err.Error(); got != "cluster not running. Run 'obol stack up' first" {
		t.Fatalf("err = %q, want cluster-down message", got)
	}
}
