package network

import (
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
)

// Memory sizing for the Ethereum clients. Each client gets a request (what
// the scheduler reserves: steady state, kept low so a mainnet and a testnet
// node fit on one machine) and a limit (where the container is OOM-killed:
// steady state plus headroom for sync, backfill and reorgs). Clients whose
// runtime doesn't see the limit on its own get told it (helmfile.yaml.gotmpl
// turns the limit into GOMEMLIMIT, JVM and Node heap sizes, and reth's
// cross-block cache), so they stay under it instead of being killed.
//
// executionMemory / consensusMemory in values.yaml (--execution-memory /
// --consensus-memory at install) set the LIMIT; the request follows the
// table, but never above the limit.

// MemorySizing is one client's memory request and limit, in MiB.
type MemorySizing struct {
	RequestMi int64
	LimitMi   int64
}

// gi is n GiB in MiB.
func gi(n float64) int64 { return int64(n * 1024) }

func sz(req, lim float64) MemorySizing { return MemorySizing{gi(req), gi(lim)} }

// elSizing: [mainnet|testnet][full|archive][client].
var elSizing = map[bool]map[bool]map[string]MemorySizing{
	true: { // mainnet
		false: {"reth": sz(4, 10), "geth": sz(6, 12), "nethermind": sz(6, 12), "besu": sz(6, 12), "erigon": sz(8, 16)},
		true:  {"reth": sz(6, 14), "geth": sz(8, 16), "nethermind": sz(8, 16), "besu": sz(8, 16), "erigon": sz(8, 16)},
	},
	false: { // hoodi, sepolia
		false: {"reth": sz(1, 4), "geth": sz(2, 6), "nethermind": sz(2, 6), "besu": sz(2, 6), "erigon": sz(3, 8)},
		true:  {"reth": sz(2, 6), "geth": sz(3, 8), "nethermind": sz(3, 8), "besu": sz(3, 8), "erigon": sz(3, 8)},
	},
}

// clSizing: [network][client]. Lighthouse peaks with backfill (mainnet
// 6.8 GiB, hoodi 5.1 GiB observed); Silver's memory has grown over days, so
// its limit also bounds that growth with a restart.
var clSizing = map[string]map[string]MemorySizing{
	"mainnet": {"lighthouse": sz(5, 10), "prysm": sz(5, 10), "teku": sz(6, 10), "lodestar": sz(5, 10), "grandine": sz(4, 8), "nimbus": sz(2, 5), "silver": sz(4, 8)},
	"hoodi":   {"lighthouse": sz(2, 6), "prysm": sz(2, 6), "teku": sz(3, 6), "lodestar": sz(2, 6), "grandine": sz(2, 5), "nimbus": sz(1, 3), "silver": sz(2, 5)},
	"sepolia": {"lighthouse": sz(2, 5), "prysm": sz(2, 5), "teku": sz(3, 6), "lodestar": sz(2, 5), "grandine": sz(2, 4), "nimbus": sz(1, 3), "silver": sz(2, 4)},
}

// Fallbacks for a client or network missing from the tables.
var (
	defaultELSizing = sz(2, 8)
	defaultCLSizing = sz(2, 6)
)

// parseMemoryMi converts a Kubernetes memory quantity to MiB.
func parseMemoryMi(s string) (int64, error) {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, fmt.Errorf("invalid memory %q: %w", s, err)
	}

	return q.Value() >> 20, nil
}

// applyOverride makes override (a quantity, "" = none) the limit, and keeps
// the request at or below it.
func applyOverride(s MemorySizing, override string) (MemorySizing, error) {
	if override == "" {
		return s, nil
	}

	mi, err := parseMemoryMi(override)
	if err != nil {
		return s, err
	}

	if mi < 256 {
		return s, fmt.Errorf("memory %q is below 256Mi", override)
	}

	s.LimitMi = mi
	s.RequestMi = min(s.RequestMi, mi)

	return s, nil
}

// EthereumSizing returns the execution and consensus clients' memory for a
// deployment's values.
func EthereumSizing(network, mode, el, cl, elOverride, clOverride string) (MemorySizing, MemorySizing, error) {
	elS, ok := elSizing[network == "mainnet"][mode == modeArchive][el]
	if !ok {
		elS = defaultELSizing
	}

	clS, ok := clSizing[network][cl]
	if !ok {
		clS = defaultCLSizing
	}

	elS, err := applyOverride(elS, elOverride)
	if err != nil {
		return elS, clS, fmt.Errorf("executionMemory: %w", err)
	}

	clS, err = applyOverride(clS, clOverride)
	if err != nil {
		return elS, clS, fmt.Errorf("consensusMemory: %w", err)
	}

	return elS, clS, nil
}

// sizingStateValues are the helmfile --state-values-set arguments that hand
// the sizing to helmfile.yaml.gotmpl.
func sizingStateValues(el, cl MemorySizing) []string {
	kv := map[string]int64{
		"elMemoryRequestMi": el.RequestMi, "elMemoryLimitMi": el.LimitMi,
		"clMemoryRequestMi": cl.RequestMi, "clMemoryLimitMi": cl.LimitMi,
	}

	var args []string
	for _, k := range []string{"elMemoryRequestMi", "elMemoryLimitMi", "clMemoryRequestMi", "clMemoryLimitMi"} {
		args = append(args, "--state-values-set", k+"="+strconv.FormatInt(kv[k], 10))
	}

	return args
}

// deploymentSizing is EthereumSizing for an installed deployment.
func deploymentSizing(d Deployment) (MemorySizing, MemorySizing, error) {
	return EthereumSizing(d.Network, d.Mode, d.ExecutionClient, d.ConsensusClient, d.ExecutionMemory, d.ConsensusMemory)
}
