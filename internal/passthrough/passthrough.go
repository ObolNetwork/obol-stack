// Package passthrough hands control from obol to an external tool (kubectl,
// helm, helmfile, k9s, or `kubectl exec` into an agent pod) with the tool's
// native behaviour intact: argv, environment, TTY, signals and exit codes.
//
// Two execution modes:
//
//   - Exec: on unix the obol process is replaced (syscall.Exec), so the tool
//     owns the terminal, receives signals directly and its exit status (or
//     signal death) is what the shell sees. On Windows it runs as a child
//     (see Run) and obol exits with the child's code.
//   - Run: the tool runs as a child process with stdio wired through. Used
//     when obol must do something after the tool exits (stop a port-forward,
//     print a hint). SIGTERM/SIGHUP sent to obol are forwarded to the child;
//     SIGINT/SIGQUIT are not, because terminal-generated signals already reach
//     the whole foreground process group (forwarding would double-deliver
//     Ctrl-C). A child killed by signal n maps to exit status 128+n.
//
// Env policy (KubeEnv): `obol <tool>` always targets the stack kubeconfig,
// overriding an ambient KUBECONFIG; only an explicit --kubeconfig on the
// command line opts out. Plain tools can be pointed at the stack with
// `obol env`.
package passthrough

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/tools"
)

// StackDownHint is printed to stderr when a tool that was pointed at the
// stack kubeconfig fails while that kubeconfig does not exist.
const StackDownHint = "hint: no stack kubeconfig found — is the stack up? run 'obol stack up'"

// ExitError carries a tool's exit status back to the CLI layer so obol can
// exit with the same code after its own deferred cleanup has run. It
// implements urfave/cli's ExitCoder; Error is empty so nothing extra is
// printed (the tool already reported its own failure).
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "" }

// ExitCode returns the tool's exit status (128+n for a signal death).
func (e *ExitError) ExitCode() int { return e.Code }

// StripSeparator removes exactly one leading "--", so `obol kubectl -- get po`
// behaves like `obol kubectl get po`. Later "--" are the tool's business.
func StripSeparator(args []string) []string {
	if len(args) > 0 && args[0] == "--" {
		return args[1:]
	}
	return args
}

// HasFlag reports whether any of flags appears in args, either bare
// ("--kubeconfig") or with an inline value ("--kubeconfig=x"). Scanning stops
// at "--": what follows belongs to a nested command (e.g. `kubectl exec`).
func HasFlag(args []string, flags ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, f := range flags {
			if a == f || strings.HasPrefix(a, f+"=") {
				return true
			}
		}
	}
	return false
}

// LookupEnv returns the value of key in env (KEY=value entries; last one
// wins, matching exec semantics). Names are case-insensitive on Windows.
func LookupEnv(env []string, key string) (string, bool) {
	val, found := "", false
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if k == key || (runtime.GOOS == "windows" && strings.EqualFold(k, key)) {
			val, found = v, true
		}
	}
	return val, found
}

// DefaultEnv returns a copy of env with key=value appended unless the user
// already chose a value: key is set (non-empty) in env, or one of flags is
// present in args. The bool reports whether the default was applied.
// Any existing (empty) key entries are replaced (see SetEnv).
func DefaultEnv(env, args []string, key, value string, flags ...string) ([]string, bool) {
	out := append([]string(nil), env...)
	if v, ok := LookupEnv(env, key); ok && v != "" {
		return out, false
	}
	if HasFlag(args, flags...) {
		return out, false
	}
	return SetEnv(out, key, value), true
}

// SetEnv returns a copy of env with every existing key entry removed and
// key=value appended (syscall.Exec passes env verbatim and getenv returns
// the first match, so duplicates must not survive).
func SetEnv(env []string, key, value string) []string {
	out := slices.DeleteFunc(append([]string(nil), env...), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return k == key || (runtime.GOOS == "windows" && strings.EqualFold(k, key))
	})
	return append(out, key+"="+value)
}

// KubeEnv applies the kubeconfig policy for `obol <tool>`: the obol prefix
// means "target the stack", so KUBECONFIG=stackKubeconfig always replaces an
// ambient KUBECONFIG. Only an explicit --kubeconfig on the command line
// opts out (env left untouched; the flag wins in the tool). --context alone
// does not change this. The bool reports whether the stack kubeconfig is used.
//
// The stack path is set even when the file does not exist yet: falling back
// to ~/.kube/config or an ambient KUBECONFIG would silently point
// `obol kubectl delete …` at some other cluster.
func KubeEnv(env, args []string, stackKubeconfig string) ([]string, bool) {
	if HasFlag(args, "--kubeconfig") {
		return append([]string(nil), env...), false
	}
	return SetEnv(env, "KUBECONFIG", stackKubeconfig), true
}

// StackHint returns StackDownHint when the tool will use the stack
// kubeconfig and that file is missing, otherwise "".
func StackHint(usesStack bool, stackKubeconfig string) string {
	if !usesStack {
		return ""
	}
	if _, err := os.Stat(stackKubeconfig); errors.Is(err, os.ErrNotExist) {
		return StackDownHint
	}
	return ""
}

// ResolveTool resolves a host tool for passthrough use. It follows the same
// order as config.ToolPath (OBOL_<TOOL> → obol bin dir → compatible $PATH),
// with one relaxation: when no managed or compatible copy exists but $PATH
// has a version-incompatible one, a warning goes to warn and that binary is
// used — the user asked to run the tool, so a skewed version beats "not
// found". Stack lifecycle commands keep the strict resolver.
func ResolveTool(binDir, name string, warn io.Writer) (string, error) {
	res := tools.Resolve(binDir, name)
	if res.Found() {
		return res.Path, nil
	}
	if res.Rejected != "" {
		pin := ""
		if t, ok := tools.Default().Get(name); ok {
			pin = " (obol pins " + t.Version + ")"
		}
		if warn != nil {
			fmt.Fprintf(warn, "warning: using %s %s from $PATH%s; run 'obol upgrade' to install a managed copy\n",
				res.Rejected, res.RejectedVersion, pin)
		}
		return res.Rejected, nil
	}
	return "", tools.MissingError(name)
}

// Handoff transfers control to the tool. With an empty hint it calls Exec
// (unix: the obol process is replaced; Handoff only returns if exec itself
// failed). With a hint, the tool runs as a child; on a non-zero exit the
// hint is printed to stderr and an *ExitError with the tool's code is
// returned.
func Handoff(path string, args, env []string, hint string) error {
	if hint == "" {
		return Exec(path, args, env)
	}
	code, err := Run(path, args, env)
	if err != nil {
		return err
	}
	if code != 0 {
		fmt.Fprintln(os.Stderr, hint)
		return &ExitError{Code: code}
	}
	return nil
}

// Run executes path as a child process with obol's stdin/stdout/stderr,
// forwarding termination signals, and returns the child's exit status
// (128+n when killed by signal n). err is non-nil only if the child could
// not be started.
func Run(path string, args, env []string) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, notifySignals...)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("run %s: %w", path, err)
	}

	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-sigs:
				if shouldForward(s) {
					_ = cmd.Process.Signal(s)
				}
			case <-done:
				return
			}
		}
	}()

	err := cmd.Wait()
	close(done)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return ExitStatus(exitErr.ProcessState), nil
	}
	return 1, err
}

// ExitStatus maps a finished process to a shell-style exit status: the exit
// code, or 128+n when the process was killed by signal n.
func ExitStatus(ps *os.ProcessState) int {
	if ps == nil {
		return 1
	}
	if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ps.ExitCode()
}
