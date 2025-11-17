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
// Automatically strips network-specific prefixes (e.g., ETHEREUM_, AZTEC_, HELIOS_)
// Example: ETHEREUM_NETWORK -> network
// Example: AZTEC_ATTESTER_PRIVATE_KEY -> attester-private-key
func envVarToFlagName(envName string) string {
	// Find the first underscore to detect network prefix pattern
	// Network-specific env vars follow pattern: NETWORK_NAME_*
	parts := strings.SplitN(envName, "_", 2)
	if len(parts) == 2 {
		// Strip the network prefix (everything before first underscore)
		envName = parts[1]
	}

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

	// Generate expected prefix from network name (e.g., "aztec" -> "AZTEC_")
	networkPrefix := strings.ToUpper(networkName) + "_"

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

			// Only include env vars that match the network prefix
			if !strings.HasPrefix(envName, networkPrefix) {
				continue
			}

			// Skip duplicates
			if seen[envName] {
				continue
			}
			seen[envName] = true

			// Convert env var name to CLI flag name (strips network prefix)
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
