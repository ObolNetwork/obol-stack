package agentruntime

import (
	"os"
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

// TestStreamsSupportTTY locks in the kubectl-mirroring gate: -t is requested
// only when BOTH stdin and stdout are terminals. The decisive regression case
// is "stdin is a terminal but stdout is a pipe" (command substitution like the
// release smoke's `$(obol buy inference …)` from a tmux pane), which previously
// added -t and crashed kubectl < 1.36 in its terminal-resize path.
func TestStreamsSupportTTY(t *testing.T) {
	// A regular file is not a character device.
	regular, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	t.Cleanup(func() { regular.Close() })

	// Pipe ends are not character devices either (they model the captured-output
	// case: stdout wired to a pipe under `$(...)`).
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { pr.Close(); pw.Close() })

	// /dev/null IS a character device — a portable stand-in for a terminal here.
	// The "char-device stdin, pipe stdout" case is the exact regression: the old
	// stdin-only gate returned true (→ -t → kubectl < 1.36 panic); the two-stream
	// gate must return false. Guarded so the table degrades gracefully if /dev/null
	// is somehow unavailable.
	devNull, dnErr := os.Open(os.DevNull)
	if dnErr == nil {
		t.Cleanup(func() { devNull.Close() })
		if !isCharDevice(devNull) {
			t.Fatalf("expected %s to be a character device", os.DevNull)
		}
	}

	type ttyCase struct {
		name string
		in   *os.File
		out  *os.File
		want bool
	}
	tests := []ttyCase{
		{"both pipes", pr, pw, false},
		{"stdin pipe, stdout regular file", pr, regular, false},
		{"stdin regular file, stdout pipe", regular, pw, false},
		{"nil stdin", nil, pw, false},
		{"nil stdout", pr, nil, false},
		{"both nil", nil, nil, false},
	}
	if devNull != nil {
		tests = append(tests,
			// Regression: terminal stdin + pipe stdout must NOT request a TTY.
			ttyCase{"char-device stdin, pipe stdout (regression)", devNull, pw, false},
			// Both terminals is the only true case.
			ttyCase{"both char devices", devNull, devNull, true},
		)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := streamsSupportTTY(tc.in, tc.out); got != tc.want {
				t.Fatalf("streamsSupportTTY(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestExecInPod_NoTTYWhenStdoutPiped is the end-to-end guarantee: when this test
// process runs (under `go test`, stdout is captured, not a terminal),
// shouldRequestTTY must be false so BuildExecArgs omits -t.
func TestExecInPod_NoTTYWhenStdoutPiped(t *testing.T) {
	if shouldRequestTTY() {
		t.Skip("test stdout is a terminal; the piped-output gate cannot be exercised here")
	}
	args := BuildExecArgs(Hermes, DefaultInstanceID, []string{"true"}, shouldRequestTTY())
	for _, a := range args {
		if a == "-t" {
			t.Fatalf("BuildExecArgs unexpectedly contains -t when stdout is not a terminal: %#v", args)
		}
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
