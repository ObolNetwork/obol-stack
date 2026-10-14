package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/network"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

var sampleNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

func u64(n uint64) *uint64 { return new(n) }

func sampleERPC(mainnetServing string) network.ERPCStatus {
	return network.ERPCStatus{
		Endpoint: network.ERPCEndpoint,
		Health:   network.ERPCHealthy,
		Pods:     []network.ERPCPod{{Name: "erpc-7c9cb85d44-5wbwt", Phase: "Running", Ready: true}},
		Chains: []network.ERPCChain{
			{Network: "mainnet", ChainID: 1, Serving: mainnetServing, Upstreams: []network.ERPCUpstream{
				{ID: "local-ethereum-mainnet", Local: true, Deployment: "ethereum/mainnet"},
				{ID: "obol-rpc-mainnet"},
			}, Attempts: network.ParseERPCUpstreamsHeader("local-ethereum-mainnet=primary:empty;obol-rpc-mainnet=sweep:success:won")},
			{Network: "base", ChainID: 8453, Serving: "obol-rpc-base", Upstreams: []network.ERPCUpstream{{ID: "obol-rpc-base"}}},
			{Network: "base-sepolia", ChainID: 84532, Serving: "base-sepolia-official", Upstreams: []network.ERPCUpstream{
				{ID: "base-sepolia-official"}, {ID: "base-sepolia-publicnode"},
			}},
			{Network: "hoodi", ChainID: 560048, Serving: "local-ethereum-hoodi", Upstreams: []network.ERPCUpstream{
				{ID: "local-ethereum-hoodi", Local: true, Deployment: "ethereum/hoodi"},
				{ID: "obol-rpc-hoodi"},
				{ID: "allnodes-rpc-hoodi"},
			}},
		},
	}
}

func mainnetDeployment() network.Deployment {
	return network.Deployment{
		Type: "ethereum", ID: "mainnet", Namespace: "ethereum-mainnet", Network: "mainnet",
		ExecutionClient: "reth", ConsensusClient: "lighthouse", Mode: "archive",
		ArchiveScope: "archive since cancun (block 19,426,587)",
	}
}

func rethStages(execution uint64) []network.SyncStage {
	const target = 26187032

	stage := func(name, state string, block uint64) network.SyncStage {
		return network.SyncStage{Name: name, State: state, Block: block, Target: target, Unit: network.UnitBlocks}
	}

	return []network.SyncStage{
		stage("Era", network.StageDone, 0),
		stage("Headers", network.StageDone, target),
		stage("Bodies", network.StageDone, target),
		stage("SenderRecovery", network.StageDone, target),
		stage("Execution", network.StageCurrent, execution),
		stage("AccountHashing", network.StagePending, 0),
		stage("StorageHashing", network.StagePending, 0),
		stage("MerkleExecute", network.StagePending, 0),
		stage("TransactionLookup", network.StagePending, 0),
		stage("IndexStorageHistory", network.StagePending, 0),
		stage("IndexAccountHistory", network.StagePending, 0),
		stage("Finish", network.StagePending, 0),
	}
}

func storage() *network.StorageStatus {
	return &network.StorageStatus{Path: "/data/ethereum-mainnet", ExecutionBytes: 412 << 30, ConsensusBytes: 38 << 30, FreeBytes: u64(1200 << 30)}
}

// syncingNode: reth in its Execution stage, lighthouse at head (optimistic).
func syncingNode() network.NodeStatus {
	el := &network.ExecutionStatus{
		Reachable: true, Syncing: true, Peers: u64(5),
		ClientVersion: "reth/v2.7.0-3d592ec/x86_64-unknown-linux-gnu", Stages: rethStages(1234567),
	}
	cl := &network.ConsensusStatus{
		Reachable: true, HeadSlot: 12904123, IsOptimistic: true, Peers: u64(15),
		Version: "Lighthouse/v8.2.3-1a2b3c4d/x86_64-linux", SyncState: "Synced",
	}
	cl.Stages = []network.SyncStage{{Name: "At head (optimistic)", State: network.StageCurrent, Block: 12904123, Unit: network.UnitSlots}}

	pods := []network.PodStatus{
		{Name: "ethereum-execution-0", Component: "execution", Phase: "Running", Ready: true, MemoryRequest: "12Gi"},
		{Name: "ethereum-beacon-0", Component: "beacon", Phase: "Running", Ready: true, MemoryRequest: "6Gi"},
	}

	d := mainnetDeployment()

	return network.NodeStatus{
		Deployment: d, Pods: pods, Problems: network.PodProblems(d, pods, sampleNow),
		Execution: el, Consensus: cl, Storage: storage(),
		ERPC:       &network.NodeERPC{Upstream: "local-ethereum-mainnet", Registered: true, Serving: "obol-rpc-mainnet"},
		GrafanaURL: network.GrafanaNodeURL(d.Namespace),
		Verdict:    network.ComputeVerdict(pods, el, cl),
	}
}

// crashingNode: the rc1 host case, reth syncing while lighthouse OOM-loops.
func crashingNode() network.NodeStatus {
	n := syncingNode()
	n.Pods[1] = network.PodStatus{
		Name: "ethereum-beacon-0", Component: "beacon", Phase: "Running", Restarts: 9,
		Reason: "CrashLoopBackOff", MemoryRequest: "6Gi",
		LastTermination: &network.Termination{Reason: "OOMKilled", ExitCode: 137, FinishedAt: sampleNow.Add(-3 * time.Minute)},
	}
	n.Consensus = &network.ConsensusStatus{Error: "not answering (pod starting or restarting?)"}
	n.Problems = network.PodProblems(n.Deployment, n.Pods, sampleNow)
	n.Verdict = network.ComputeVerdict(n.Pods, n.Execution, n.Consensus)

	return n
}

func syncedNode() network.NodeStatus {
	d := network.Deployment{
		Type: "ethereum", ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi",
		ExecutionClient: "geth", ConsensusClient: "lighthouse", Mode: "full", ArchiveScope: "full",
	}
	el := &network.ExecutionStatus{Reachable: true, HeadBlock: 1834001, Peers: u64(48), ClientVersion: "Geth/v1.17.8-stable-a1b2c3d4/linux-amd64/go1.25"}
	cl := &network.ConsensusStatus{
		Reachable: true, HeadSlot: 1950321, Peers: u64(72), Version: "Lighthouse/v8.2.3-1a2b3c4d/x86_64-linux", SyncState: "BackFillSyncing",
		Stages: []network.SyncStage{
			{Name: "At head", State: network.StageDone, Block: 1950321, Unit: network.UnitSlots},
			{Name: "Backfill", State: network.StageCurrent, Block: 400000, Target: 1950000, Unit: network.UnitSlots},
		},
	}
	pods := []network.PodStatus{
		{Name: "ethereum-execution-0", Component: "execution", Phase: "Running", Ready: true},
		{Name: "ethereum-beacon-0", Component: "beacon", Phase: "Running", Ready: true},
	}

	return network.NodeStatus{
		Deployment: d, Pods: pods, Problems: []network.PodProblem{}, Execution: el, Consensus: cl,
		Storage:    &network.StorageStatus{Path: "/data/ethereum-hoodi", ExecutionBytes: 61 << 30, ConsensusBytes: 9 << 30, FreeBytes: u64(1200 << 30)},
		ERPC:       &network.NodeERPC{Upstream: "local-ethereum-hoodi", Registered: true, Serving: "local-ethereum-hoodi"},
		GrafanaURL: network.GrafanaNodeURL(d.Namespace),
		Verdict:    network.ComputeVerdict(pods, el, cl),
	}
}

func stoppedNode() network.NodeStatus {
	d := mainnetDeployment()
	d.Stopped = true
	n := network.Unprobed(d, "")
	n.Storage = storage()
	n.ERPC = &network.NodeERPC{Upstream: "local-ethereum-mainnet", Serving: "obol-rpc-mainnet"}

	return n
}

func render(r network.StatusReport, o renderOptions) string {
	fillUpstreamCounts(&r)
	return renderStatus(r, o)
}

func TestRenderStatus_Syncing(t *testing.T) {
	r := network.StatusReport{Time: sampleNow, ERPC: sampleERPC("obol-rpc-mainnet"), Nodes: []network.NodeStatus{syncingNode()}}
	out := render(r, renderOptions{})
	t.Log("\n" + out)

	for _, want := range []string{
		"==> RPC gateway (eRPC)  ·  http://obol.stack:8080/rpc/<network>  ·  healthy",
		"● local ethereum/mainnet (syncing)",
		"base-sepolia-official, +1",
		"==> Local node  ethereum/mainnet  ·  reth + lighthouse  ·  archive since cancun (block 19,426,587)",
		"◐ Syncing 4.7% — not serving RPC yet (eRPC falls back to obol-rpc-mainnet)",
		"Execution  reth v2.7.0  ·  5 peers",
		"✓ Headers              26,187,032 / 26,187,032",
		"▸ Execution             1,234,567 / 26,187,032    4.7%",
		"· pending: AccountHashing, StorageHashing, MerkleExecute, TransactionLookup, +3 more",
		"Consensus  lighthouse v8.2.3  ·  15 peers",
		"exec 412 GiB · cons 38 GiB · 1.2 TiB free",
		"Grafana    http://obol.stack:8080/grafana/d/obol-eth-node?var-namespace=ethereum-mainnet",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}

	for _, leak := range []string{"127.0.0.1", "x86_64", "syncing=", "erpc-7c9cb85d44"} {
		if strings.Contains(out, leak) {
			t.Errorf("non-verbose output leaks %q", leak)
		}
	}

	if len(r.Unhealthy()) != 0 {
		t.Error("a syncing node is not unhealthy")
	}

	verbose := render(r, renderOptions{Verbose: true})
	t.Log("\n" + verbose)

	for _, want := range []string{
		"reth/v2.7.0-3d592ec/x86_64-unknown-linux-gnu", "is_optimistic=true", "erpc-7c9cb85d44-5wbwt",
		"base-sepolia-publicnode", "tried: local-ethereum-mainnet primary→empty, obol-rpc-mainnet sweep→success (won)",
		"ethereum-beacon-0 Running ready", "AccountHashing", "MerkleExecute",
	} {
		if !strings.Contains(verbose, want) {
			t.Errorf("verbose missing %q", want)
		}
	}
}

func TestRenderStatus_WatchRates(t *testing.T) {
	n := syncingNode()
	rate, eta := 410.0, int64(60900)
	n.Execution.Stages[4].RatePerSec, n.Execution.Stages[4].ETASeconds = &rate, &eta

	out := render(network.StatusReport{ERPC: sampleERPC("obol-rpc-mainnet"), Nodes: []network.NodeStatus{n}}, renderOptions{Watch: true})
	if !strings.Contains(out, "4.7%  ·  410 blk/s  ·  ETA 16h55m") {
		t.Fatalf("rate/ETA missing:\n%s", out)
	}

	if !strings.Contains(out, "▸ At head (optimistic)  slot 12,904,123  ·  measuring rate…") {
		t.Fatalf("watch without a rate yet:\n%s", out)
	}
}

func TestRenderStatus_Synced(t *testing.T) {
	r := network.StatusReport{ERPC: sampleERPC("obol-rpc-mainnet"), Nodes: []network.NodeStatus{syncedNode()}}
	out := render(r, renderOptions{})
	t.Log("\n" + out)

	for _, want := range []string{
		"hoodi         560048  ● local ethereum/hoodi (synced)  local ethereum/hoodi",
		"local ethereum/hoodi\n", // serving now
		"✓ Synced — head block 1,834,001; serving /rpc/hoodi",
		"Execution  geth v1.17.8-stable  ·  48 peers",
		"✓ At head  block 1,834,001",
		"✓ At head   slot 1,950,321",
		"▸ Backfill    400,000 / 1,950,000   20.5%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestRenderStatus_Stopped(t *testing.T) {
	r := network.StatusReport{ERPC: sampleERPC("obol-rpc-mainnet"), Nodes: []network.NodeStatus{stoppedNode()}}
	out := render(r, renderOptions{})
	t.Log("\n" + out)

	if !strings.Contains(out, "◼ Stopped (data kept, 450 GiB) — start with: obol network start ethereum/mainnet") {
		t.Fatalf("stopped headline:\n%s", out)
	}

	if strings.Contains(out, "Execution  ") || len(r.Unhealthy()) != 0 {
		t.Fatalf("stopped nodes are neither probed nor unhealthy:\n%s", out)
	}
}

func TestRenderStatus_CrashingBeacon(t *testing.T) {
	r := network.StatusReport{ERPC: sampleERPC("obol-rpc-mainnet"), Nodes: []network.NodeStatus{crashingNode()}}
	out := render(r, renderOptions{})
	t.Log("\n" + out)

	for _, want := range []string{
		"● local ethereum/mainnet (degraded)",
		"✗ Beacon node restarting (OOMKilled); the execution client cannot follow the chain without it",
		"✗ Restarting — OOMKilled 9× (last 3m ago)",
		"Hint: Out of memory (request 6Gi, no memory limit)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}

	// One report of the OOM, no raw "unreachable" next to it.
	if strings.Count(out, "OOMKilled 9×") != 1 || strings.Contains(out, "Beacon API not answering") {
		t.Errorf("duplicate or raw beacon error:\n%s", out)
	}

	if bad := r.Unhealthy(); len(bad) != 1 || bad[0] != "ethereum/mainnet" {
		t.Fatalf("unhealthy = %v, want exit 3 for the crashing node", bad)
	}

	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	t.Log("\n" + string(raw))

	for _, want := range []string{`"state": "degraded"`, `"problems": [`, `"message": "Restarting — OOMKilled 9× (last 3m ago)"`, `"stages": [`, `"chains": [`, `"pods": [`, `"archive_scope"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %s", want)
		}
	}
}

func TestShortClientVersion(t *testing.T) {
	cases := map[string]string{
		"reth/v2.7.0-3d592ec/x86_64-unknown-linux-gnu":    "reth v2.7.0",
		"Lighthouse/v8.2.3-1a2b3c4d/x86_64-linux":         "lighthouse v8.2.3",
		"Geth/v1.17.8-stable-a1b2c3d4/linux-amd64/go1.25": "geth v1.17.8-stable",
		"Nethermind/v2.1.0+abcdef01/linux-x64/dotnet9":    "nethermind v2.1.0",
		"teku/v26.10.0/linux-x86_64":                      "teku v26.10.0",
		"custom":                                          "custom",
	}
	for in, want := range cases {
		if got := shortClientVersion(in); got != want {
			t.Errorf("shortClientVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEtaAndRateLabels(t *testing.T) {
	if got := etaLabel(30); got != "<1m" {
		t.Error(got)
	}
	if got := etaLabel(16*3600 + 5*60); got != "16h05m" {
		t.Error(got)
	}
	if got := etaLabel(3*86400 + 4*3600); got != "3d04h" {
		t.Error(got)
	}
	if got := rateLabel(1.25, network.UnitSlots); got != "1.2 slots/s" {
		t.Error(got)
	}
	if got := rateLabel(12345, network.UnitBlocks); got != "12,345 blk/s" {
		t.Error(got)
	}
}

func TestPhysicalLines(t *testing.T) {
	frame := "short\n" + strings.Repeat("x", 25) + "\n" + ui.Paint("ok", "colored") + "\n"
	if got := physicalLines(frame, 10); got != 5 { // 1 + 3 + 1
		t.Fatalf("physicalLines = %d", got)
	}

	if got := physicalLines(frame, 0); got != 3 {
		t.Fatalf("unknown width = %d", got)
	}
}

func TestNetworkStatus_WatchNeedsTerminal(t *testing.T) {
	root := newRootCommand(newTestConfig(t))

	err := root.Run(context.Background(), []string{"obol", "network", "status", "--watch"})
	if err == nil || !strings.Contains(err.Error(), "--watch needs a terminal") {
		t.Fatalf("err = %v", err)
	}
}

// --watch under -o json: one compact JSON object per tick, forwards kept
// open across ticks and closed on exit.
func TestWatchStatus_NDJSON(t *testing.T) {
	beacon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/eth/v1/node/syncing" {
			_, _ = w.Write([]byte(`{"data":{"head_slot":"100","sync_distance":"50","is_syncing":true}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer beacon.Close()

	var opened, closed atomic.Int32
	prober := &network.NodeProber{
		Forward: func(context.Context, string, string, int) (string, func(), error) {
			opened.Add(1)
			return beacon.URL, func() { closed.Add(1) }, nil
		},
		Pods: func(context.Context, string) ([]network.PodStatus, error) { return []network.PodStatus{}, nil },
		HTTP: beacon.Client(),
	}

	d := network.Deployment{Type: "ethereum", ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi"}

	var out bytes.Buffer
	u := ui.NewWithAllOptions(false, false, ui.OutputJSON)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	err := watchStatus(ctx, u, prober, func() []network.Deployment { return []network.Deployment{d} },
		&network.StatusOptions{}, 100*time.Millisecond, &out, func() int { return 0 })
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("want one JSON line per tick, got %d:\n%s", len(lines), out.String())
	}

	for _, l := range lines {
		var r network.StatusReport
		if err := json.Unmarshal([]byte(l), &r); err != nil || len(r.Nodes) != 1 {
			t.Fatalf("bad NDJSON line %q: %v", l, err)
		}
	}

	// EL + CL forwards (the EL "forward" answers with the beacon fake, so it
	// fails and is reopened each tick); the CL one is reused.
	if closed.Load() != opened.Load() {
		t.Fatalf("forwards left open: opened=%d closed=%d", opened.Load(), closed.Load())
	}

	if int(opened.Load()) >= 2*len(lines) {
		t.Fatalf("CL forward not reused across ticks: opened=%d for %d ticks", opened.Load(), len(lines))
	}
}
