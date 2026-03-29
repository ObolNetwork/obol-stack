package openclaw

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ListInstanceIDs returns the IDs of all installed OpenClaw instances by
// reading the deployment directories on disk.
func ListInstanceIDs(cfg *config.Config) ([]string, error) {
	appsDir := filepath.Join(cfg.ConfigDir, "applications", appName)

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to read OpenClaw instances: %w", err)
	}

	var ids []string

	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}

	return ids, nil
}

// ResolveInstance determines which OpenClaw instance to target based on how
// many instances are installed:
//
//   - 0 instances: returns an error prompting the user to create one
//   - 1 instance:  auto-selects it, returns args unchanged
//   - 2+ instances: expects args[0] to be a known instance name; consumes it
//     from args and returns the rest. Errors if no match.
func ResolveInstance(cfg *config.Config, args []string) (id string, remaining []string, err error) {
	instances, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", nil, err
	}

	switch len(instances) {
	case 0:
		return "", nil, errors.New("no OpenClaw instances found — run 'obol agent init' to create one")
	case 1:
		return instances[0], args, nil
	default:
		if len(args) > 0 {
			for _, inst := range instances {
				if args[0] == inst {
					return inst, args[1:], nil
				}
			}
		}

		return "", nil, fmt.Errorf("multiple OpenClaw instances found, specify one: %s", strings.Join(instances, ", "))
	}
}
