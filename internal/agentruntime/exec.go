package agentruntime

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// BuildExecArgs returns the kubectl argv for `kubectl exec` into the agent
// pod identified by (runtime, id), running argv inside it. Pure / testable —
// no side effects. argv is the full in-pod command vector; argv[0] is the
// binary (e.g. "python3" or a runtime CLI path), argv[1:] its args.
//
// withTTY toggles the `-t` flag; callers usually pass stdinIsTerminal().
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
// the bundled kubectl binary. Stdin/stdout/stderr are wired to the host TTY.
//
// On non-zero exit from the in-pod command, ExecInPod calls os.Exit with the
// same status to preserve exit codes for shell scripting. A nil return means
// the command exited 0.
func ExecInPod(cfg *config.Config, runtime Runtime, id string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("ExecInPod: argv is empty")
	}

	kubeconfigPath := filepath.Join(cfg.ConfigDir, "kubeconfig.yaml")
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return errors.New("cluster not running. Run 'obol stack up' first")
	}

	kubectlBinary := filepath.Join(cfg.BinDir, "kubectl")

	cmd := exec.Command(kubectlBinary, BuildExecArgs(runtime, id, argv, stdinIsTerminal())...)
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				os.Exit(status.ExitStatus())
			}
		}
		return fmt.Errorf("kubectl exec into %s/%s: %w", Namespace(runtime, id), Describe(runtime).ServiceName, err)
	}
	return nil
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
