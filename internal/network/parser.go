package network

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// EnvVar represents an environment variable with its default value
type EnvVar struct {
	Name         string
	DefaultValue string
	FlagName     string // CLI flag name derived from env var name
}

// parseHelmfileEnvVars extracts environment variables from a helmfile
// It looks for patterns like: {{ env "VAR_NAME" | default "value" }}
func parseHelmfileEnvVars(helmfilePath string) ([]EnvVar, error) {
	content, err := os.ReadFile(helmfilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read helmfile: %w", err)
	}

	// Regex to match: {{ env "VAR_NAME" | default "value" }}
	// Captures: VAR_NAME and value
	re := regexp.MustCompile(`{{\s*env\s+"([^"]+)"\s*\|\s*default\s+"([^"]*)"\s*}}`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	if len(matches) == 0 {
		return nil, nil
	}

	var envVars []EnvVar
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) != 3 {
			continue
		}

		envName := match[1]
		defaultValue := match[2]

		// Skip duplicates
		if seen[envName] {
			continue
		}
		seen[envName] = true

		// Convert env var name to CLI flag name
		// Example: ETHEREUM_NETWORK -> --network
		// Example: ETHEREUM_EXECUTION_CLIENT -> --execution-client
		flagName := envVarToFlagName(envName)

		envVars = append(envVars, EnvVar{
			Name:         envName,
			DefaultValue: defaultValue,
			FlagName:     flagName,
		})
	}

	return envVars, nil
}

// envVarToFlagName converts an environment variable name to a CLI flag name
// Example: ETHEREUM_NETWORK -> network
// Example: ETHEREUM_EXECUTION_CLIENT -> execution-client
func envVarToFlagName(envName string) string {
	// Remove common network prefix if present
	envName = strings.TrimPrefix(envName, "ETHEREUM_")
	envName = strings.TrimPrefix(envName, "HELIOS_")

	// Convert to lowercase and replace underscores with hyphens
	flagName := strings.ToLower(envName)
	flagName = strings.ReplaceAll(flagName, "_", "-")

	return flagName
}

// getNetworkHelmfilePath returns the path to a network's helmfile
func getNetworkHelmfilePath(configDir, network string) string {
	return filepath.Join(configDir, "networks", network, "helmfile.yaml")
}

// ParseEmbeddedNetworkEnvVars extracts environment variables from an embedded network helmfile
func ParseEmbeddedNetworkEnvVars(networkName string) ([]EnvVar, error) {
	// Read the embedded helmfile
	content, err := embed.ReadEmbeddedNetworkFile(networkName, "helmfile.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded helmfile: %w", err)
	}

	// Regex to match: {{ env "VAR_NAME" | default "value" }}
	// Captures: VAR_NAME and value
	re := regexp.MustCompile(`{{\s*env\s+"([^"]+)"\s*\|\s*default\s+"([^"]*)"\s*}}`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	if len(matches) == 0 {
		return nil, nil
	}

	var envVars []EnvVar
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(match) != 3 {
			continue
		}

		envName := match[1]
		defaultValue := match[2]

		// Skip duplicates
		if seen[envName] {
			continue
		}
		seen[envName] = true

		// Convert env var name to CLI flag name
		flagName := envVarToFlagName(envName)

		envVars = append(envVars, EnvVar{
			Name:         envName,
			DefaultValue: defaultValue,
			FlagName:     flagName,
		})
	}

	return envVars, nil
}
