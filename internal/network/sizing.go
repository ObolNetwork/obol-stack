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

// elSizing: [mainnet|testnet][full|archive][client]. Limits sit about 1.5x
// above the peak anonymous memory (cadvisor container_memory_rss) of Obol's
// own nodes, Nov 2026: geth hoodi 8.4 GiB, besu hoodi 5.8, nethermind hoodi
// 4.6 (OOM-killed once at a 7 GiB limit), geth / nethermind mainnet archive
// 11.4 / 10.9, reth mainnet 6.8 (jemalloc; its RSS is mostly MDBX mmap,
// which is reclaimable page cache and doesn't count towards an OOM).
var elSizing = map[bool]map[bool]map[string]MemorySizing{
	true: { // mainnet
		false: {elReth: sz(4, 10), elGeth: sz(6, 12), elNethermind: sz(6, 12), elBesu: sz(6, 12), elErigon: sz(8, 16)},
		true:  {elReth: sz(6, 14), elGeth: sz(8, 16), elNethermind: sz(8, 16), elBesu: sz(8, 16), elErigon: sz(8, 16)},
	},
	false: { // hoodi, sepolia
		false: {elReth: sz(1, 4), elGeth: sz(3, 10), elNethermind: sz(3, 8), elBesu: sz(3, 8), elErigon: sz(3, 8)},
		true:  {elReth: sz(2, 6), elGeth: sz(4, 12), elNethermind: sz(4, 10), elBesu: sz(4, 10), elErigon: sz(4, 10)},
	},
}

// clSizing: [network][client]. Hoodi's validator set is as large as
// mainnet's, so its beacon state, and the beacon node's memory, are too:
// hoodi gets mainnet's limits. Requests follow a node with no or few
// validators (custody of 4-8 column groups: Lighthouse peaked at 6.8 GiB on
// mainnet, 5.1 on hoodi), and stay lower on hoodi so a testnet node still
// schedules on a small Docker VM. Limits cover a supernode (all 128 groups,
// which validators worth 4096 ETH or more attached to the node bring), from
// Obol's own hoodi and mainnet beacon nodes, Nov 2026 peak RSS: Lighthouse
// 12.3 (OOM-killed once at 14), Teku 10.6, Prysm 9.7 median, Lodestar 7.0,
// Grandine 6.9, Nimbus 5.3. Silver's memory has grown over days, so its
// limit also bounds that growth with a restart.
var clSizing = map[string]map[string]MemorySizing{
	netMainnet: {clLighthouse: sz(5, 16), clPrysm: sz(5, 14), clTeku: sz(6, 14), clLodestar: sz(4, 12), clGrandine: sz(4, 10), clNimbus: sz(3, 8), clSilver: sz(4, 8)},
	netHoodi:   {clLighthouse: sz(3, 16), clPrysm: sz(3, 14), clTeku: sz(4, 14), clLodestar: sz(3, 12), clGrandine: sz(3, 10), clNimbus: sz(2, 8), clSilver: sz(3, 8)},
	netSepolia: {clLighthouse: sz(2, 6), clPrysm: sz(2, 6), clTeku: sz(3, 8), clLodestar: sz(2, 6), clGrandine: sz(2, 5), clNimbus: sz(1, 4), clSilver: sz(2, 5)},
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
