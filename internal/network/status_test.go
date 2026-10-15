package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// reth's eth_syncing during the staged pipeline: current/highest are 0
// ("syncing 0/0" before), the real story is in stages.
func TestParseEthSyncing_RethStages(t *testing.T) {
	var el ExecutionStatus
	if err := parseEthSyncing(rpcResult(t, "reth_syncing_stages.json"), &el); err != nil {
		t.Fatal(err)
	}

	if len(el.Stages) != 15 {
		t.Fatalf("stages = %d", len(el.Stages))
	}

	const target = 0x18f9518 // 26,187,032

	states := map[string]string{}
	for _, s := range el.Stages {
		states[s.Name] = s.State
		if s.Target != target {
			t.Fatalf("%s target = %d, want the Headers checkpoint %d", s.Name, s.Target, target)
		}
	}

	want := map[string]string{
		"Era":            StageDone, // 0, but a later stage passed it (skipped)
		"Headers":        StageDone,
		"Bodies":         StageDone,
		"SenderRecovery": StageDone,
		"Execution":      StageCurrent,
		"AccountHashing": StagePending,
		"Finish":         StagePending,
	}
	for name, st := range want {
		if states[name] != st {
			t.Errorf("%s = %q, want %q", name, states[name], st)
		}
	}

	el.Reachable = true
	pct, ok := elProgress(&el)
	if !ok || pct != 4.7 { // 1,234,567 / 26,187,032
		t.Fatalf("progress = %v %v, want 4.7%% from the Execution stage", pct, ok)
	}

	v := ComputeVerdict(nil, &el, &ConsensusStatus{Reachable: true, HeadSlot: 1, IsOptimistic: true})
	if v.ProgressPct == nil || *v.ProgressPct != 4.7 {
		t.Fatalf("verdict progress = %v", v.ProgressPct)
	}
}

func TestAnnotateExecutionStages_HeadersDownloading(t *testing.T) {
	el := ExecutionStatus{Stages: []SyncStage{{Name: "Headers"}, {Name: "Bodies"}, {Name: "Execution"}}}
	annotateExecutionStages(&el)

	if el.Stages[0].State != StageCurrent || el.Stages[1].State != StagePending || el.Stages[2].State != StagePending {
		t.Fatalf("stages = %+v", el.Stages)
	}

	el.Reachable, el.Syncing = true, true
	if _, ok := elProgress(&el); ok {
		t.Fatal("no target yet: progress must be unknown, not 0%")
	}
}

func TestLighthouseStages(t *testing.T) {
	cases := []struct {
		fixture, state string
		want           []SyncStage
	}{
		{"lighthouse_backfill.json", "BackFillSyncing", []SyncStage{
			{Name: clStageSynced, State: StageDone, Block: 12904123, Unit: UnitSlots},
			{Name: clStageBackfill, State: StageCurrent, Block: 1048576, Target: 12582912, Unit: UnitSlots},
		}},
		{"lighthouse_syncing_head.json", "SyncingHead", []SyncStage{
			{Name: clStageFinalized, State: StageDone, Unit: UnitSlots},
			{Name: clStageHead, State: StageCurrent, Block: 12904123, Target: 12904123, Unit: UnitSlots},
			{Name: clStageBackfill, State: StagePending, Unit: UnitSlots},
		}},
		{"lighthouse_synced.json", "Synced", []SyncStage{
			{Name: clStageSynced, State: StageDone, Block: 12904123, Unit: UnitSlots},
		}},
	}

	for _, c := range cases {
		cl := &ConsensusStatus{HeadSlot: 12904123}
		if err := parseLighthouseSyncing(fixture(t, c.fixture), cl); err != nil {
			t.Fatalf("%s: %v", c.fixture, err)
		}

		if cl.SyncState != c.state {
			t.Fatalf("%s: state %q", c.fixture, cl.SyncState)
		}

		if got := consensusStages(cl); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: stages\n got %+v\nwant %+v", c.fixture, got, c.want)
		}
	}

	// Other clients: head sync from the standard API.
	got := consensusStages(&ConsensusStatus{HeadSlot: 900, SyncDistance: 100, IsSyncing: true})
	if len(got) != 1 || got[0].Name != clStageHead || got[0].Target != 1000 || got[0].State != StageCurrent {
		t.Fatalf("generic syncing stages = %+v", got)
	}
}

func TestProbeBeaconAPI_LighthouseOnly(t *testing.T) {
	var lighthouseCalls atomic.Int32

	srv := func(version string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/eth/v1/node/syncing":
				_, _ = w.Write(fixture(t, "beacon_synced.json"))
			case "/eth/v1/node/version":
				_, _ = w.Write([]byte(`{"data":{"version":"` + version + `"}}`))
			case "/lighthouse/syncing":
				lighthouseCalls.Add(1)
				_, _ = w.Write(fixture(t, "lighthouse_backfill.json"))
			default:
				http.NotFound(w, r)
			}
		}))
	}

	lh := srv("Lighthouse/v8.2.3-abcdef1/x86_64-linux")
	defer lh.Close()

	cl := &ConsensusStatus{}
	ProbeBeaconAPI(context.Background(), lh.Client(), lh.URL, cl)
	if cl.SyncState != "BackFillSyncing" || len(cl.Stages) != 2 || cl.Stages[1].Name != clStageBackfill {
		t.Fatalf("lighthouse status = %+v", cl)
	}

	teku := srv("teku/v26.10.0/linux-x86_64")
	defer teku.Close()

	cl = &ConsensusStatus{}
	ProbeBeaconAPI(context.Background(), teku.Client(), teku.URL, cl)
	if lighthouseCalls.Load() != 1 || cl.SyncState != "" || cl.Stages[0].Name != clStageSynced {
		t.Fatalf("teku must not get /lighthouse/syncing: calls=%d status=%+v", lighthouseCalls.Load(), cl)
	}
}

func TestPodProblems(t *testing.T) {
	pods, err := parsePodList(fixture(t, "pods_oom.json"))
	if err != nil {
		t.Fatal(err)
	}

	beacon := pods[1]
	if beacon.LastTermination == nil || beacon.LastTermination.Reason != "OOMKilled" || beacon.MemoryRequest != "6Gi" {
		t.Fatalf("beacon pod = %+v", beacon)
	}

	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	d := Deployment{Type: "ethereum", ID: "mainnet", Namespace: "ethereum-mainnet"}

	got := PodProblems(d, pods, now)
	if len(got) != 2 {
		t.Fatalf("problems = %+v, want beacon + unschedulable snapshotter (execution is healthy)", got)
	}

	oom := got[0]
	if oom.Component != "beacon" || oom.State != ProblemRestarting || oom.Message != "Restarting — OOMKilled 9× (last 3m ago)" {
		t.Fatalf("oom problem = %+v", oom)
	}

	for _, want := range []string{"request 6Gi", "no memory limit", "consensusMemory", "obol network sync ethereum/mainnet"} {
		if !strings.Contains(oom.Hint, want) {
			t.Errorf("hint %q missing %q", oom.Hint, want)
		}
	}

	pending := got[1]
	if pending.State != ProblemPending || pending.Message != "Pending — can't be scheduled: Insufficient memory" {
		t.Fatalf("pending problem = %+v", pending)
	}

	// Recovered: ready again, restarted recently → a warning, not severe.
	rec := PodProblems(d, []PodStatus{{
		Name: "b", Component: "beacon", Ready: true, Restarts: 2,
		LastTermination: &Termination{Reason: "OOMKilled", FinishedAt: now.Add(-10 * time.Minute)},
	}}, now)
	if len(rec) != 1 || rec[0].State != ProblemRestarted || rec[0].Severe() {
		t.Fatalf("recovered = %+v", rec)
	}

	// Old restarts on a ready pod are history, not a problem.
	if old := PodProblems(d, []PodStatus{{
		Name: "b", Component: "beacon", Ready: true, Restarts: 2,
		LastTermination: &Termination{Reason: "Error", FinishedAt: now.Add(-5 * time.Hour)},
	}}, now); len(old) != 0 {
		t.Fatalf("old restart = %+v", old)
	}
}

// The live rc1 case: reth syncing, lighthouse OOM-looping. That used to read
// as "syncing"; reth can't follow the chain without its beacon node.
func TestComputeVerdict_CrashingBeaconDegrades(t *testing.T) {
	pods, _ := parsePodList(fixture(t, "pods_oom.json"))
	el := &ExecutionStatus{Reachable: true, Syncing: true}
	_ = parseEthSyncing(rpcResult(t, "reth_syncing_stages.json"), el)

	v := ComputeVerdict(pods, el, &ConsensusStatus{Error: "not answering"})
	if v.State != VerdictDegraded || !strings.Contains(v.Summary, "beacon node restarting (OOMKilled)") {
		t.Fatalf("verdict = %+v", v)
	}

	if !VerdictUnhealthy(v.State) || VerdictUnhealthy(VerdictSyncing) || VerdictUnhealthy(VerdictStopped) {
		t.Fatal("VerdictUnhealthy mapping")
	}
}

func TestParseERPCUpstreamsHeader(t *testing.T) {
	got := ParseERPCUpstreamsHeader("local-ethereum-mainnet=primary:empty;obol-rpc-mainnet=sweep:success:won")
	want := []ERPCAttempt{
		{Upstream: "local-ethereum-mainnet", Phase: "primary", Result: "empty"},
		{Upstream: "obol-rpc-mainnet", Phase: "sweep", Result: "success", Won: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}

	if s := servingUpstream(got, ""); s != "obol-rpc-mainnet" {
		t.Fatalf("serving = %q", s)
	}

	if s := servingUpstream(nil, "obol-rpc-base"); s != "obol-rpc-base" {
		t.Fatalf("single-header serving = %q", s)
	}

	if ParseERPCUpstreamsHeader("") != nil {
		t.Fatal("empty header must parse to nil")
	}
}

func testERPCConfig() map[string]any {
	return map[string]any{"projects": []any{map[string]any{
		"id": "rpc",
		"networks": []any{
			map[string]any{"evm": map[string]any{"chainId": 1}, "alias": "mainnet"},
			map[string]any{"evm": map[string]any{"chainId": 8453}, "alias": "base"},
		},
		"upstreams": []any{
			map[string]any{"id": "local-ethereum-mainnet", "evm": map[string]any{"chainId": 1}},
			map[string]any{"id": "obol-rpc-mainnet", "evm": map[string]any{"chainId": 1}},
			map[string]any{"id": "obol-rpc-base", "evm": map[string]any{"chainId": 8453}},
			map[string]any{"id": "custom-999", "evm": map[string]any{"chainId": 999}},
		},
	}}}
}

func TestERPCChainsFromConfig(t *testing.T) {
	project, chains := ERPCChainsFromConfig(testERPCConfig(), []Deployment{{Type: "ethereum", ID: "mainnet"}})
	if project != "rpc" || len(chains) != 3 {
		t.Fatalf("project %q chains %+v", project, chains)
	}

	main := chains[0]
	if main.Network != "mainnet" || len(main.Upstreams) != 2 || !main.Upstreams[0].Local || main.Upstreams[0].Deployment != "ethereum/mainnet" {
		t.Fatalf("mainnet = %+v", main)
	}

	if chains[1].Network != "chain-999" || chains[2].Network != "base" {
		t.Fatalf("chains not sorted by id, or alias lost: %+v", chains)
	}
}

// fakeERPC answers eth_blockNumber per chain path with eRPC's headers.
func fakeERPC(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Erpc-Skip-Cache-Read") != "true" {
			t.Errorf("probe must bypass the eRPC cache")
		}

		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "eth_blockNumber") {
			t.Errorf("body = %s", body)
		}

		switch r.URL.Path {
		case "/rpc/mainnet":
			w.Header().Set("X-Erpc-Upstreams", "local-ethereum-mainnet=primary:empty;obol-rpc-mainnet=sweep:success:won")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x18f9518"}`))
		case "/rpc/base":
			w.Header().Set("X-Erpc-Upstream", "obol-rpc-base")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
		case "/rpc/evm/999":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32603,"message":"all upstreams failed"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// End to end with fakes: eRPC config + serving probe, node probes and the
// link between them.
func TestGather(t *testing.T) {
	el := fakeEL(t, "reth_syncing_stages.json")
	defer el.Close()
	cl := fakeBeacon(t, "beacon_optimistic.json")
	defer cl.Close()
	erpc := fakeERPC(t)
	defer erpc.Close()

	var opened atomic.Int32
	prober := &NodeProber{
		Forward: func(_ context.Context, ns, svc string, _ int) (string, func(), error) {
			opened.Add(1)
			switch svc {
			case executionService:
				return el.URL, func() {}, nil
			case beaconService:
				return cl.URL, func() {}, nil
			case erpcService:
				return erpc.URL, func() {}, nil
			}
			return "", nil, errors.New("unknown service " + ns + "/" + svc)
		},
		Pods: func(_ context.Context, ns string) ([]PodStatus, error) {
			if ns == erpcNamespace {
				return []PodStatus{{Name: "erpc-1", Component: "erpc", Phase: "Running", Ready: true}}, nil
			}
			return parsePodList(fixture(t, "pods.json"))
		},
		ERPCConfig: func(context.Context) (map[string]any, error) { return testERPCConfig(), nil },
		HTTP:       &http.Client{Timeout: 2 * time.Second},
	}

	deps := []Deployment{
		{Type: "ethereum", ID: "mainnet", Namespace: "ethereum-mainnet", Network: "mainnet"},
		{Type: "ethereum", ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi", Stopped: true},
	}

	r := prober.Gather(context.Background(), deps, StatusOptions{})

	if r.ERPC.Health != ERPCHealthy || len(r.ERPC.Chains) != 3 {
		t.Fatalf("erpc = %+v", r.ERPC)
	}

	if c := r.ERPC.Chains[0]; c.Serving != "obol-rpc-mainnet" || len(c.Attempts) != 2 {
		t.Fatalf("mainnet serving = %+v", c)
	}

	if c := r.ERPC.Chains[2]; c.Serving != "obol-rpc-base" {
		t.Fatalf("base serving = %+v", c)
	}

	if c := r.ERPC.Chains[1]; c.Serving != "" || !strings.Contains(c.ProbeError, "all upstreams failed") {
		t.Fatalf("chain-999 = %+v", c)
	}

	main := r.Nodes[0]
	if main.ERPC == nil || !main.ERPC.Registered || main.ERPC.Serving != "obol-rpc-mainnet" {
		t.Fatalf("mainnet eRPC link = %+v", main.ERPC)
	}

	if main.Verdict.State != VerdictSyncing || main.GrafanaURL != "http://obol.stack:8080/grafana/d/obol-eth-node?var-namespace=ethereum-mainnet" {
		t.Fatalf("mainnet = %+v", main.Verdict)
	}

	stopped := r.Nodes[1]
	if stopped.Verdict.State != VerdictStopped || stopped.Execution != nil || stopped.ERPC.Registered {
		t.Fatalf("stopped node must not be probed: %+v", stopped)
	}

	if opened.Load() != 3 {
		t.Fatalf("opened %d forwards, want EL + CL + eRPC (none for the stopped node)", opened.Load())
	}

	if bad := r.Unhealthy(); len(bad) != 0 {
		t.Fatalf("unhealthy = %v", bad)
	}

	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`"stages":[`, `"serving":"obol-rpc-mainnet"`, `"problems":[`, `"pods":[`, `"stopped":true`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("JSON missing %s", want)
		}
	}

	// --no-probe: no forwards at all, eRPC config still read.
	opened.Store(0)
	r = prober.Gather(context.Background(), deps, StatusOptions{NoProbe: true})
	if opened.Load() != 0 || len(r.ERPC.Chains) != 3 || r.Nodes[0].Verdict.State != VerdictNotProbed || r.Nodes[1].Verdict.State != VerdictStopped {
		t.Fatalf("no-probe: opened=%d report=%+v", opened.Load(), r)
	}
}

func TestNodeProberSession_ReusesAndReopensForwards(t *testing.T) {
	var opened, closed atomic.Int32

	healthy := atomic.Bool{}
	healthy.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		_, _ = w.Write(fixture(t, "beacon_synced.json"))
	}))
	defer srv.Close()

	p := &NodeProber{
		Forward: func(context.Context, string, string, int) (string, func(), error) {
			opened.Add(1)
			return srv.URL, func() { closed.Add(1) }, nil
		},
		HTTP: srv.Client(),
	}

	p.StartSession(context.Background())

	for range 3 {
		if cl := p.probeConsensus(context.Background(), "ns"); !cl.Reachable {
			t.Fatalf("probe failed: %s", cl.Error)
		}
	}

	if opened.Load() != 1 || closed.Load() != 0 {
		t.Fatalf("opened=%d closed=%d, want one forward reused", opened.Load(), closed.Load())
	}

	healthy.Store(false)
	_ = p.probeConsensus(context.Background(), "ns")
	healthy.Store(true)
	_ = p.probeConsensus(context.Background(), "ns")

	if opened.Load() != 2 || closed.Load() != 1 {
		t.Fatalf("opened=%d closed=%d, want the broken forward replaced", opened.Load(), closed.Load())
	}

	p.Close()

	if closed.Load() != 2 {
		t.Fatalf("Close left forwards open: closed=%d", closed.Load())
	}
}

func TestRateTracker(t *testing.T) {
	r := NewRateTracker()
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

	node := func(exec, slot, target uint64) []NodeStatus {
		return []NodeStatus{{
			Namespace: "ethereum-mainnet",
			Execution: &ExecutionStatus{Stages: []SyncStage{
				{Name: "Headers", State: StageDone, Block: 1000000, Target: 1000000},
				{Name: "Execution", State: StageCurrent, Block: exec, Target: 1000000},
			}},
			Consensus: &ConsensusStatus{Stages: []SyncStage{{Name: clStageHead, State: StageCurrent, Block: slot, Target: target}}},
		}}
	}

	first := node(0, 1000, 2000)
	r.Annotate(first, t0)
	if first[0].Execution.Stages[1].RatePerSec != nil {
		t.Fatal("one sample: no rate yet")
	}

	second := node(4000, 1100, 2001) // +4000 blocks, CL +100 slots vs target +1, in 10s
	r.Annotate(second, t0.Add(10*time.Second))

	ex := second[0].Execution.Stages[1]
	if ex.RatePerSec == nil || *ex.RatePerSec != 400 || ex.ETASeconds == nil || *ex.ETASeconds != 2490 {
		t.Fatalf("execution rate = %v eta = %v", ex.RatePerSec, ex.ETASeconds)
	}

	if second[0].Execution.Stages[0].RatePerSec != nil {
		t.Fatal("done stages get no rate")
	}

	cs := second[0].Consensus.Stages[0]
	if cs.ETASeconds == nil || *cs.ETASeconds != 91 { // 901 slots left, closing at 9.9/s
		t.Fatalf("consensus eta = %v (moving target)", cs.ETASeconds)
	}

	// A stage that goes backwards (unwind) restarts its window.
	third := node(100, 1200, 2002)
	r.Annotate(third, t0.Add(20*time.Second))
	if third[0].Execution.Stages[1].RatePerSec != nil {
		t.Fatal("rate after an unwind must be re-measured")
	}
}

func TestAnnotateExecutionStages_AlphabeticalFromReth(t *testing.T) {
	// reth v2.7 eth_syncing.stages, as returned (alphabetical), mid-Execution.
	const tip = 26187032
	in := []struct {
		name  string
		block uint64
	}{
		{"AccountHashing", 0},
		{"Bodies", tip},
		{"Era", 0},
		{"Execution", 6349777},
		{"Finish", 0},
		{"Headers", tip},
		{"IndexAccountHistory", 0},
		{"IndexStorageHistory", 0},
		{"MerkleExecute", 0},
		{"MerkleUnwind", 0},
		{"Prune", 0},
		{"PruneSenderRecovery", 0},
		{"SenderRecovery", tip},
		{"StorageHashing", 0},
		{"TransactionLookup", 0},
	}

	el := ExecutionStatus{}
	for _, s := range in {
		el.Stages = append(el.Stages, SyncStage{Name: s.name, Block: s.block})
	}

	annotateExecutionStages(&el)

	var order []string
	states := map[string]string{}
	for _, s := range el.Stages {
		order = append(order, s.Name)
		states[s.Name] = s.State
	}

	if !reflect.DeepEqual(order, rethPipelineOrder) {
		t.Fatalf("order = %v", order)
	}

	for name, want := range map[string]string{
		"Era": StageDone, "Headers": StageDone, "Bodies": StageDone, "SenderRecovery": StageDone,
		"Execution": StageCurrent, "AccountHashing": StagePending, "StorageHashing": StagePending,
		"TransactionLookup": StagePending, "Finish": StagePending,
	} {
		if states[name] != want {
			t.Errorf("%s = %q, want %q", name, states[name], want)
		}
	}
}

func TestRateTracker_CheckpointPausesAreNotStalls(t *testing.T) {
	// reth's Execution checkpoint only moves on commit: a flat minute is
	// normal, a flat five minutes is worth flagging.
	r := NewRateTracker()
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	stage := func() []NodeStatus {
		return []NodeStatus{{Namespace: "n", Execution: &ExecutionStatus{Stages: []SyncStage{
			{Name: "Execution", State: StageCurrent, Block: 6349777, Target: 26187032},
		}}}}
	}

	for _, at := range []time.Duration{0, 30 * time.Second, time.Minute} {
		n := stage()
		r.Annotate(n, t0.Add(at))
		if n[0].Execution.Stages[0].RatePerSec != nil {
			t.Fatalf("at %v: rate %v, want none yet", at, *n[0].Execution.Stages[0].RatePerSec)
		}
	}

	n := stage()
	r.Annotate(n, t0.Add(6*time.Minute))
	if got := n[0].Execution.Stages[0].RatePerSec; got == nil || *got != 0 {
		t.Fatalf("after 6 flat minutes: rate %v, want 0 (stalled?)", got)
	}
}

func TestStorageStatusJSON_UnreadableIsNull(t *testing.T) {
	free := uint64(5)
	got, err := json.Marshal(StorageStatus{Path: "/d", ConsensusBytes: 7, FreeBytes: &free, Partial: true})
	if err != nil {
		t.Fatal(err)
	}

	want := `{"path":"/d","execution_bytes":null,"consensus_bytes":7,"free_bytes":5,"partial":true}`
	if string(got) != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}

	got, _ = json.Marshal(StorageStatus{Path: "/d"})
	if want := `{"path":"/d","execution_bytes":0,"consensus_bytes":0}`; string(got) != want {
		t.Fatalf("complete zero sizes: got %s, want %s", got, want)
	}
}
