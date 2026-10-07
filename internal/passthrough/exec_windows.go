//go:build windows

package passthrough

import "os"

// notifySignals: Ctrl-C/Ctrl-Break reach every process attached to the
// console, so obol only needs to survive them while the child handles its own.
var notifySignals = []os.Signal{os.Interrupt}

func shouldForward(os.Signal) bool { return false }

// Exec has no process-replacement primitive on Windows: the tool runs as a
// child (stdio wired, exit code preserved) and obol exits with its status.
func Exec(path string, args, env []string) error {
	code, err := Run(path, args, env)
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil // unreachable
}
