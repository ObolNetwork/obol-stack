package network

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/embed"
)

// EnvVar represents an environment variable with its default value
type EnvVar struct {
	Name         string
	DefaultValue string
	FlagName     string   // CLI flag name derived from env var name
	Description  string   // Human-readable description from @description
	EnumValues   []string // Valid enum values from @enum
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

// ParseEmbeddedNetworkEnvVars extracts environment variables from an embedded network helmfile
func ParseEmbeddedNetworkEnvVars(networkName string) ([]EnvVar, error) {
	// Read the embedded helmfile
	content, err := embed.ReadEmbeddedNetworkFile(networkName, "helmfile.yaml.gotmpl")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded helmfile: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	var envVars []EnvVar
	seen := make(map[string]bool)

	// Track annotations from preceding comment lines
	var currentEnum []string
	var currentDesc string

	for _, line := range lines {
		// Parse @enum annotation
		if enumMatch := regexp.MustCompile(`#\s*@enum\s+(.+)`).FindStringSubmatch(line); enumMatch != nil {
			enumStr := strings.TrimSpace(enumMatch[1])
			currentEnum = strings.Split(enumStr, ",")
			for i := range currentEnum {
				currentEnum[i] = strings.TrimSpace(currentEnum[i])
			}
			continue
		}

		// Parse @description annotation
		if descMatch := regexp.MustCompile(`#\s*@description\s+(.+)`).FindStringSubmatch(line); descMatch != nil {
			currentDesc = strings.TrimSpace(descMatch[1])
			continue
		}

		// Parse env var line: {{ env "VAR_NAME" | default "value" }}
		re := regexp.MustCompile(`{{\s*env\s+"([^"]+)"\s*\|\s*default\s+"([^"]*)"\s*}}`)
		if envMatch := re.FindStringSubmatch(line); envMatch != nil {
			envName := envMatch[1]
			defaultValue := envMatch[2]

			// Skip duplicates
			if seen[envName] {
				continue
			}
			seen[envName] = true

			// Convert env var name to CLI flag name
			flagName := envVarToFlagName(envName)

			envVar := EnvVar{
				Name:         envName,
				DefaultValue: defaultValue,
				FlagName:     flagName,
				Description:  currentDesc,
				EnumValues:   currentEnum,
			}
			envVars = append(envVars, envVar)

			// Reset annotations for next variable
			currentEnum = nil
			currentDesc = ""
		}
	}

	return envVars, nil
}
