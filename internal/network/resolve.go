package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// ListInstanceIDs returns all installed network deployment identifiers (as
// "network/id" strings) by walking the networks directory on disk.
func ListInstanceIDs(cfg *config.Config) ([]string, error) {
	networksDir := filepath.Join(cfg.ConfigDir, "networks")

	networkDirs, err := os.ReadDir(networksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read networks directory: %w", err)
	}

	var ids []string
	for _, networkDir := range networkDirs {
		if !networkDir.IsDir() {
			continue
		}
		deployments, err := os.ReadDir(filepath.Join(networksDir, networkDir.Name()))
		if err != nil {
			continue
		}
		for _, deployment := range deployments {
			if deployment.IsDir() {
				ids = append(ids, fmt.Sprintf("%s/%s", networkDir.Name(), deployment.Name()))
			}
		}
	}
	return ids, nil
}

// ResolveInstance determines which network deployment to target based on how
// many deployments are installed:
//
//   - 0 deployments: returns an error prompting the user to install one
//   - 1 deployment:  auto-selects it, returns args unchanged
//   - 2+ deployments: expects args[0] to be a known "network/id" identifier;
//     consumes it from args and returns the rest. Errors if no match.
func ResolveInstance(cfg *config.Config, args []string) (identifier string, remaining []string, err error) {
	instances, err := ListInstanceIDs(cfg)
	if err != nil {
		return "", nil, err
	}

	switch len(instances) {
	case 0:
		return "", nil, fmt.Errorf("no network deployments found — run 'obol network install <network>' to create one")
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
		return "", nil, fmt.Errorf("multiple network deployments found, specify one: %s", strings.Join(instances, ", "))
	}
}
