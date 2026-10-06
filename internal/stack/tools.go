package stack

import (
	"context"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/tools"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// ensureRequiredTools installs missing required tools into cfg.BinDir
// (download + sha256 verify). Tools already resolvable via OBOL_<TOOL>, the
// bin dir or a compatible $PATH copy are left alone; outdated managed copies
// are only replaced by `obol upgrade`. k3d is skipped on the k3s backend.
// Runs the same in interactive and non-interactive mode; a failed download
// aborts with an actionable error.
func ensureRequiredTools(cfg *config.Config, u *ui.UI, backendName string) error {
	var skip []string
	if backendName != BackendK3d {
		skip = append(skip, "k3d")
	}

	return tools.EnsureUI(context.Background(), u, cfg.BinDir, tools.EnsureOptions{
		Names: tools.Default().RequiredNames(skip...),
	})
}
