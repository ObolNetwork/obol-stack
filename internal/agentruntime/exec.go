package agentruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/passthrough"
)

// BuildExecArgs returns the kubectl argv for `kubectl exec` into the agent
// pod identified by (runtime, id), running argv inside it. Pure / testable —
// no side effects. argv is the full in-pod command vector; argv[0] is the
// binary (e.g. "python3" or a runtime CLI path), argv[1:] its args.
//
// withTTY toggles the `-t` flag; callers usually pass shouldRequestTTY().
func BuildExecArgs(runtime Runtime, id string, argv []string, withTTY bool) []string {
	svc := Describe(runtime).ServiceName
	out := []string{"exec", "-i"}
	if withTTY {
		out = append(out, "-t")
	}
	out = append(out,
		"-c", svc,
		"-n", Namespace(runtime, id),
		"deploy/"+svc,
		"--",
	)
	return append(out, argv...)
}

// ExecInPod runs argv inside the agent pod identified by (runtime, id) using
// the resolved kubectl binary, handing the terminal over to it (see
// internal/passthrough: on unix obol is replaced by kubectl, so TTY, signals
// and exit codes are native). `-t` is requested only when both stdin and
// stdout are terminals.
//
// KUBECONFIG is always the stack kubeconfig (an ambient KUBECONFIG is
// ignored: the agent pod only exists in the stack). Exec into a pod always
// needs a cluster, so a missing stack kubeconfig is reported up front instead
// of letting kubectl fail. The in-pod exit status becomes obol's exit status.
func ExecInPod(cfg *config.Config, runtime Runtime, id string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("ExecInPod: argv is empty")
	}

	kubectlBinary, err := passthrough.ResolveTool(cfg.BinDir, "kubectl", os.Stderr)
	if err != nil {
		return err
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	env := passthrough.SetEnv(os.Environ(), "KUBECONFIG", kubeconfigPath)
	if passthrough.StackHint(true, kubeconfigPath) != "" {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	args := BuildExecArgs(runtime, id, argv, shouldRequestTTY())
	if err := passthrough.Exec(kubectlBinary, args, env); err != nil {
		var exitErr *passthrough.ExitError
		if errors.As(err, &exitErr) {
			return err
		}
		return fmt.Errorf("kubectl exec into %s/%s: %w", Namespace(runtime, id), Describe(runtime).ServiceName, err)
	}
	return nil
}

// shouldRequestTTY reports whether `kubectl exec` should be invoked with -t.
// It mirrors kubectl's own setupTTY decision: a TTY is allocated only when BOTH
// the process stdin AND stdout are terminals.
//
// Gating on stdin alone is wrong. When obol is invoked under command
// substitution — e.g. the release smoke's `buy_output=$(obol buy inference …)`
// run from a tmux pane — stdin is a pty but stdout is a pipe. Requesting -t in
// that case is harmful on two counts:
//   - it corrupts captured output with TTY line-ending/escape semantics, and
//   - kubectl < 1.36 panics with a nil-pointer dereference in its
//     terminal-resize path (terminalSizeQueueAdapter.Next on a nil receiver)
//     when -t is set but stdout is not a terminal.
//
// Requiring both streams to be terminals avoids both and matches kubectl.
func shouldRequestTTY() bool {
	return streamsSupportTTY(os.Stdin, os.Stdout)
}

// streamsSupportTTY is the pure predicate behind shouldRequestTTY: both streams
// must be character devices (terminals). Factored out so it is unit-testable
// without touching the process-wide os.Stdin/os.Stdout.
func streamsSupportTTY(in, out *os.File) bool {
	return isCharDevice(in) && isCharDevice(out)
}

func isCharDevice(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
