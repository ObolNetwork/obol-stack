package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ListInstanceIDs returns all installed app deployment identifiers (as
// "app/id" strings) by walking the applications directory on disk.
func ListInstanceIDs(cfg *config.Config) ([]string, error) {
	appsDir := filepath.Join(cfg.ConfigDir, "applications")

	appDirs, err := os.ReadDir(appsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read applications directory: %w", err)
	}

	var ids []string
	for _, appDir := range appDirs {
		if !appDir.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(appsDir, appDir.Name()))
		if err != nil {
			continue
		}
		for _, deployment := range deployments {
			if !deployment.IsDir() {
				continue
			}
			// Only include directories that contain a values.yaml — this
			// distinguishes app deployments from other subsystems (e.g.
			// openclaw) that share the applications/ parent directory.
			valuesPath := filepath.Join(appsDir, appDir.Name(), deployment.Name(), "values.yaml")
			if _, err := os.Stat(valuesPath); err != nil {
				continue
			}
			ids = append(ids, fmt.Sprintf("%s/%s", appDir.Name(), deployment.Name()))
		}
	}
	return ids, nil
}

// ResolveInstance determines which app deployment to target based on how
// many deployments are installed:
//
//   - 0 deployments: returns an error prompting the user to install one
//   - 1 deployment:  auto-selects it, returns args unchanged
//   - 2+ deployments: expects args[0] to be a known "app/id" identifier;
//     consumes it from args and returns the rest. Errors if no match.
func ResolveInstance(cfg *config.Config, args []string) (identifier string, remaining []string, err error) {
	instances, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", nil, err
	}

	switch len(instances) {
	case 0:
		return "", nil, fmt.Errorf("no app deployments found — run 'obol app install <chart>' to create one")
	case 1:
		return instances[0], args, nil
	default:
		if len(args) > 0 {
			// Exact match: "postgresql/eager-fox"
			for _, inst := range instances {
				if args[0] == inst {
					return inst, args[1:], nil
				}
			}
			// Type-prefix match: "postgresql" → auto-select if only one of that type
			var prefixMatches []string
			for _, inst := range instances {
				if typ, _, ok := strings.Cut(inst, "/"); ok && typ == args[0] {
					prefixMatches = append(prefixMatches, inst)
				}
			}
			if len(prefixMatches) == 1 {
				return prefixMatches[0], args[1:], nil
			}
		}
		return "", nil, fmt.Errorf("multiple app deployments found, specify one: %s", strings.Join(instances, ", "))
	}
}
