package tunnel

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

const stackIDFile = ".stack-id"

func getStackID(cfg *config.Config) string {
	data, err := os.ReadFile(filepath.Join(cfg.ConfigDir, stackIDFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

