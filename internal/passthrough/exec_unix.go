//go:build !windows

package passthrough

import (
	"fmt"
	"os"
	"syscall"
)

// notifySignals are caught while a child runs (see Run). SIGINT and SIGQUIT
// are caught only so they do not kill obol: the terminal already delivers
// them to the child, which shares obol's foreground process group.
var notifySignals = []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGHUP}

func shouldForward(s os.Signal) bool {
	return s == syscall.SIGTERM || s == syscall.SIGHUP
}

// Exec replaces the obol process with path (argv[0] = path). The tool keeps
// obol's PID, terminal and stdio, so signals and exit status are native. It
// returns only when the exec itself fails.
func Exec(path string, args, env []string) error {
	argv := append([]string{path}, args...)
	if err := syscall.Exec(path, argv, env); err != nil {
		return fmt.Errorf("exec %s: %w", path, err)
	}
	return nil // unreachable
}
