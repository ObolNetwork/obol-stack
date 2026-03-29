package network

import "testing"

func TestValidateInstallOptions_AllowsDisabledIndexer(t *testing.T) {
	values := map[string]string{
		"ExecutionClient":              "geth",
		"RethIndexerEnabled":           "false",
		"RethImageRepository":          "",
		"RethImageTag":                 "",
		"RethIndexerPort":              "8088",
		"RethIndexerDbPath":            "/data/erc8004-indexer/indexer.db",
		"RethIndexerRegistryAddress":   "0x8004A818BFB912233c491871b3d84c89A494BD9e",
		"RethIndexerBackfillFromBlock": "0",
	}

	if err := validateInstallOptions("ethereum", values); err != nil {
		t.Fatalf("validateInstallOptions returned error for disabled indexer: %v", err)
	}
}

func TestValidateInstallOptions_RequiresRethExecutionClient(t *testing.T) {
	values := map[string]string{
		"ExecutionClient":              "geth",
		"RethIndexerEnabled":           "true",
		"RethImageRepository":          "ghcr.io/example/reth-indexer",
		"RethImageTag":                 "latest",
		"RethIndexerPort":              "8088",
		"RethIndexerDbPath":            "/data/erc8004-indexer/indexer.db",
		"RethIndexerRegistryAddress":   "0x8004A818BFB912233c491871b3d84c89A494BD9e",
		"RethIndexerBackfillFromBlock": "0",
	}

	if err := validateInstallOptions("ethereum", values); err == nil {
		t.Fatal("expected validation error when indexer is enabled on a non-reth client")
	}
}

func TestValidateInstallOptions_RequiresCustomImage(t *testing.T) {
	values := map[string]string{
		"ExecutionClient":              "reth",
		"RethIndexerEnabled":           "true",
		"RethImageRepository":          "",
		"RethImageTag":                 "",
		"RethIndexerPort":              "8088",
		"RethIndexerDbPath":            "/data/erc8004-indexer/indexer.db",
		"RethIndexerRegistryAddress":   "0x8004A818BFB912233c491871b3d84c89A494BD9e",
		"RethIndexerBackfillFromBlock": "0",
	}

	if err := validateInstallOptions("ethereum", values); err == nil {
		t.Fatal("expected validation error when custom image flags are missing")
	}
}

func TestValidateInstallOptions_RejectsInvalidPort(t *testing.T) {
	values := map[string]string{
		"ExecutionClient":              "reth",
		"RethIndexerEnabled":           "true",
		"RethImageRepository":          "ghcr.io/example/reth-indexer",
		"RethImageTag":                 "latest",
		"RethIndexerPort":              "99999",
		"RethIndexerDbPath":            "/data/erc8004-indexer/indexer.db",
		"RethIndexerRegistryAddress":   "0x8004A818BFB912233c491871b3d84c89A494BD9e",
		"RethIndexerBackfillFromBlock": "0",
	}

	if err := validateInstallOptions("ethereum", values); err == nil {
		t.Fatal("expected validation error for invalid indexer port")
	}
}
