package network

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func validateInstallOptions(networkName string, values map[string]string) error { //nolint:unparam // networkName will vary when more networks gain validation
	if networkName != "ethereum" {
		return nil
	}

	rethIndexerEnabled, err := parseBoolString(values["RethIndexerEnabled"])
	if err != nil {
		return fmt.Errorf("invalid --reth-indexer-enabled value %q: %w", values["RethIndexerEnabled"], err)
	}

	if !rethIndexerEnabled {
		return nil
	}

	if values["ExecutionClient"] != "reth" {
		return errors.New("the embedded ERC-8004 indexer requires --execution-client reth")
	}

	if strings.TrimSpace(values["RethImageRepository"]) == "" || strings.TrimSpace(values["RethImageTag"]) == "" {
		return errors.New("the embedded ERC-8004 indexer requires both --reth-image-repository and --reth-image-tag")
	}

	port := strings.TrimSpace(values["RethIndexerPort"])
	if port != "" {
		numericPort, err := strconv.Atoi(port)
		if err != nil || numericPort <= 0 || numericPort > 65535 {
			return fmt.Errorf("invalid --reth-indexer-port value %q", values["RethIndexerPort"])
		}
	}

	if strings.TrimSpace(values["RethIndexerDbPath"]) == "" {
		return errors.New("the embedded ERC-8004 indexer requires --reth-indexer-db-path")
	}

	if strings.TrimSpace(values["RethIndexerRegistryAddress"]) == "" {
		return errors.New("the embedded ERC-8004 indexer requires --reth-indexer-registry-address")
	}

	if backfill := strings.TrimSpace(values["RethIndexerBackfillFromBlock"]); backfill != "" {
		if _, err := strconv.ParseUint(backfill, 10, 64); err != nil {
			return fmt.Errorf("invalid --reth-indexer-backfill-from-block value %q", values["RethIndexerBackfillFromBlock"])
		}
	}

	return nil
}

func parseBoolString(value string) (bool, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "false", "0", "no":
		return false, nil
	case "true", "1", "yes":
		return true, nil
	default:
		return false, errors.New("expected true/false")
	}
}
