package network

import (
	"fmt"
	"math"
)

// ethereumStorageProfile is the single source of truth for Ethereum local
// node storage sizing. The profile is intentionally conservative because
// local-path storage does not reserve bytes up front, but sized storage
// classes do enforce these requests.
type ethereumStorageProfile struct {
	ExecutionSize     string
	ConsensusSize     string
	DiskRequirementGB uint64
	Label             string
}

func resolveEthereumStorageProfile(network, mode, executionClient string, scope ArchiveScope) ethereumStorageProfile {
	if mode != "archive" {
		if network == "mainnet" {
			return ethereumStorageProfile{
				ExecutionSize:     "500Gi",
				ConsensusSize:     "200Gi",
				DiskRequirementGB: 700,
				Label:             "full mainnet",
			}
		}
		return ethereumStorageProfile{
			ExecutionSize:     "100Gi",
			ConsensusSize:     "50Gi",
			DiskRequirementGB: 150,
			Label:             "full testnet",
		}
	}

	if network != "mainnet" {
		return ethereumStorageProfile{
			ExecutionSize:     "300Gi",
			ConsensusSize:     "100Gi",
			DiskRequirementGB: 400,
			Label:             "archive testnet",
		}
	}

	if !partialArchiveClients[executionClient] || scope.Kind == "" || scope.Kind == "all" {
		return ethereumStorageProfile{
			ExecutionSize:     "4500Gi",
			ConsensusSize:     "500Gi",
			DiskRequirementGB: 5000,
			Label:             "archive mainnet from genesis",
		}
	}

	switch scope.Kind {
	case "before":
		if hf := hardforkProfileForBlock(scope.Block); hf != nil {
			execGi := roundUpGiB(uint64(math.Ceil(hf.ApproxArchiveSizeTB * 1024 * 1.2)))
			return ethereumStorageProfile{
				ExecutionSize:     formatGi(execGi),
				ConsensusSize:     "500Gi",
				DiskRequirementGB: execGi + 700,
				Label:             "partial archive mainnet " + hf.Name,
			}
		}

		// A raw block before the oldest known partial-archive preset could
		// retain almost all history, so size it as a full archive.
		return ethereumStorageProfile{
			ExecutionSize:     "4500Gi",
			ConsensusSize:     "500Gi",
			DiskRequirementGB: 5000,
			Label:             "custom archive mainnet block",
		}
	case "distance":
		execGi := executionGiForDistance(scope.Distance)
		return ethereumStorageProfile{
			ExecutionSize:     formatGi(execGi),
			ConsensusSize:     "500Gi",
			DiskRequirementGB: diskRequirementForMainnetArchive(execGi),
			Label:             "partial archive mainnet distance",
		}
	default:
		return ethereumStorageProfile{
			ExecutionSize:     "4500Gi",
			ConsensusSize:     "500Gi",
			DiskRequirementGB: 5000,
			Label:             "archive mainnet",
		}
	}
}

func hardforkProfileForBlock(block uint64) *Hardfork {
	var matched *Hardfork
	for i := range MainnetHardforks {
		if MainnetHardforks[i].Block <= block {
			matched = &MainnetHardforks[i]
			continue
		}
		break
	}
	return matched
}

func diskRequirementForMainnetArchive(execGi uint64) uint64 {
	if execGi >= 4500 {
		return 5000
	}
	return execGi + 700
}

func executionGiForDistance(distance uint64) uint64 {
	days := float64(distance*12) / float64(24*60*60)
	execGi := uint64(math.Ceil(500 + days*0.82))
	if execGi < 500 {
		execGi = 500
	}
	if execGi > 4500 {
		execGi = 4500
	}
	return roundUpGiB(execGi)
}

func roundUpGiB(gib uint64) uint64 {
	const step = 100
	if gib%step == 0 {
		return gib
	}
	return ((gib / step) + 1) * step
}

func formatGi(gib uint64) string {
	return fmt.Sprintf("%dGi", gib)
}
