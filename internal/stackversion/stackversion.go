// Package stackversion records which obol version last brought the stack up,
// so a different CLI version can say the update is not finished yet.
package stackversion

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/fsutil"
	"github.com/ObolNetwork/obol-stack/internal/version"
)

// stackVersionFile holds the obol version of the last successful
// `obol stack up`. obolup.sh reads it too.
const stackVersionFile = ".stack-version"

// stackIDFile marks an initialized stack (internal/stack writes it).
const stackIDFile = ".stack-id"

// Record notes that this CLI version brought the stack up. Best effort: a
// missing record only means a spurious "finish the update".
func Record(cfg *config.Config) {
	_ = fsutil.WriteSecret(filepath.Join(cfg.ConfigDir, stackVersionFile), []byte(version.Short()+"\n"))
}

// Skew compares the version that last brought the stack up with this
// CLI. stackVersion is "" for stacks last brought up before the record
// existed (obol < 0.17.1). It never reports skew without an initialized
// stack, or for development builds.
func Skew(cfg *config.Config) (stackVersion string, skewed bool) {
	if _, err := os.Stat(filepath.Join(cfg.ConfigDir, stackIDFile)); err != nil {
		return "", false
	}

	cli := version.Short()
	if cli == "" || cli == "dev" {
		return "", false
	}

	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, stackVersionFile))
	if err != nil {
		return "", true
	}

	stackVersion = strings.TrimSpace(string(data))
	if stackVersion == "dev" {
		return stackVersion, false
	}

	return stackVersion, stackVersion != cli
}

// SkewMessage describes a skew reported by Skew.
func SkewMessage(stackVersion string) string {
	from := "an earlier obol release"
	if stackVersion != "" {
		from = "obol " + stackVersion
	}

	return "The stack was last brought up by " + from + "; this is obol " + version.Short() +
		". Run 'obol stack up' to finish the update"
}
