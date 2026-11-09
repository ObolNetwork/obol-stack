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

// Networks and clients the sizing tables know.
const (
	netMainnet = "mainnet"
	netHoodi   = "hoodi"
	netSepolia = "sepolia"

	elReth       = "reth"
	elGeth       = "geth"
	elNethermind = "nethermind"
	elBesu       = "besu"
	elErigon     = "erigon"

	clLighthouse = "lighthouse"
	clPrysm      = "prysm"
	clTeku       = "teku"
	clLodestar   = "lodestar"
	clGrandine   = "grandine"
	clNimbus     = "nimbus"
	clSilver     = "silver"
)

// gi is n GiB in MiB.
func gi(n float64) int64 { return int64(n * 1024) }

func sz(req, lim float64) MemorySizing { return MemorySizing{gi(req), gi(lim)} }

// elSizing: [mainnet|testnet][full|archive][client].
var elSizing = map[bool]map[bool]map[string]MemorySizing{
	true: { // mainnet
		false: {elReth: sz(4, 10), elGeth: sz(6, 12), elNethermind: sz(6, 12), elBesu: sz(6, 12), elErigon: sz(8, 16)},
		true:  {elReth: sz(6, 14), elGeth: sz(8, 16), elNethermind: sz(8, 16), elBesu: sz(8, 16), elErigon: sz(8, 16)},
	},
	false: { // hoodi, sepolia
		false: {elReth: sz(1, 4), elGeth: sz(2, 6), elNethermind: sz(2, 6), elBesu: sz(2, 6), elErigon: sz(3, 8)},
		true:  {elReth: sz(2, 6), elGeth: sz(3, 8), elNethermind: sz(3, 8), elBesu: sz(3, 8), elErigon: sz(3, 8)},
	},
}

// clSizing: [network][client]. Lighthouse peaks with backfill (mainnet
// 6.8 GiB, hoodi 5.1 GiB observed); Silver's memory has grown over days, so
// its limit also bounds that growth with a restart.
var clSizing = map[string]map[string]MemorySizing{
	netMainnet: {clLighthouse: sz(5, 10), clPrysm: sz(5, 10), clTeku: sz(6, 10), clLodestar: sz(5, 10), clGrandine: sz(4, 8), clNimbus: sz(2, 5), clSilver: sz(4, 8)},
	netHoodi:   {clLighthouse: sz(2, 6), clPrysm: sz(2, 6), clTeku: sz(3, 6), clLodestar: sz(2, 6), clGrandine: sz(2, 5), clNimbus: sz(1, 3), clSilver: sz(2, 5)},
	netSepolia: {clLighthouse: sz(2, 5), clPrysm: sz(2, 5), clTeku: sz(3, 6), clLodestar: sz(2, 5), clGrandine: sz(2, 4), clNimbus: sz(1, 3), clSilver: sz(2, 4)},
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
	elS, ok := elSizing[network == netMainnet][mode == modeArchive][el]
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

// warnLowMemoryOverrides flags executionMemory / consensusMemory values
// below the client's table request. Before they became limits they were
// requests, and obol's Pending hint said to lower them; as a limit, such a
// value gets the client OOM-killed in a loop.
func lowMemoryOverrides(d Deployment) []string {
	el, cl, err := EthereumSizing(d.Network, d.Mode, d.ExecutionClient, d.ConsensusClient, "", "")
	if err != nil {
		return nil
	}

	var out []string
	for _, o := range []struct {
		key, client, value string
		table              MemorySizing
	}{
		{"executionMemory", d.ExecutionClient, d.ExecutionMemory, el},
		{"consensusMemory", d.ConsensusClient, d.ConsensusMemory, cl},
	} {
		if o.value == "" {
			continue
		}

		if mi, err := parseMemoryMi(o.value); err == nil && mi < o.table.RequestMi {
			out = append(out, fmt.Sprintf("%s: %s is now %s's memory LIMIT, below what it needs at steady state (%s); it will be OOM-killed. "+
				"Raise it to at least %s, or remove it to use the default (%s).",
				o.key, o.value, o.client, formatMi(o.table.RequestMi), formatMi(o.table.RequestMi), formatMi(o.table.LimitMi)))
		}
	}

	return out
}
