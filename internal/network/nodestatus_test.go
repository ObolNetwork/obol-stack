package network

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", "nodestatus", name))
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func rpcResult(t *testing.T, name string) json.RawMessage {
	t.Helper()

	var env struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(fixture(t, name), &env); err != nil {
		t.Fatal(err)
	}

	return env.Result
}

func TestParseEthSyncing(t *testing.T) {
	var el ExecutionStatus
	if err := parseEthSyncing(rpcResult(t, "reth_syncing.json"), &el); err != nil {
		t.Fatal(err)
	}
	if !el.Syncing || el.CurrentBlock != 0x10c8e0 || el.HighestBlock != 0x21a4c0 {
		t.Fatalf("reth syncing object parsed as %+v", el)
	}

	el = ExecutionStatus{Syncing: true}
	if err := parseEthSyncing(rpcResult(t, "geth_synced.json"), &el); err != nil || el.Syncing {
		t.Fatalf("false result: syncing=%v err=%v", el.Syncing, err)
	}

	if err := parseEthSyncing(json.RawMessage(`"weird"`), &el); err == nil {
		t.Fatal("unexpected eth_syncing shape accepted")
	}
}

func TestParseBeaconSyncing(t *testing.T) {
	cases := map[string]ConsensusStatus{
		"beacon_syncing.json":        {HeadSlot: 1200000, SyncDistance: 600, IsSyncing: true},
		"beacon_synced.json":         {HeadSlot: 1750123},
		"beacon_optimistic.json":     {HeadSlot: 1750123, SyncDistance: 1, IsOptimistic: true},
		"beacon_el_offline.json":     {HeadSlot: 1750000, SyncDistance: 123, IsSyncing: true, IsOptimistic: true, ELOffline: true},
		"beacon_legacy_numeric.json": {HeadSlot: 42},
	}
	for name, want := range cases {
		var got ConsensusStatus
		if err := parseBeaconSyncing(fixture(t, name), &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}

	n, err := parseBeaconPeerCount(fixture(t, "beacon_peer_count.json"))
	if err != nil || n != 56 {
		t.Fatalf("peer count = %d, %v", n, err)
	}
}

func TestParsePodList(t *testing.T) {
	pods, err := parsePodList(fixture(t, "pods.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 3 {
		t.Fatalf("got %d pods", len(pods))
	}
	if p := pods[0]; p.Component != "execution" || !p.Ready || p.Restarts != 0 {
		t.Errorf("execution pod = %+v", p)
	}
	if p := pods[1]; p.Component != "beacon" || p.Ready || p.Restarts != 7 || p.Reason != "CrashLoopBackOff" {
		t.Errorf("beacon pod = %+v", p)
	}
	if p := pods[2]; p.Ready || p.Phase != "Pending" {
		t.Errorf("pending pod without container statuses must not be ready: %+v", p)
	}
}

func TestComputeVerdict(t *testing.T) {
	running := []PodStatus{{Name: "ethereum-execution-0", Component: "execution", Phase: "Running", Ready: true}}
	elSynced := &ExecutionStatus{Reachable: true, HeadBlock: 21000000, Peers: new(uint64(50))}
	elSyncing := &ExecutionStatus{Reachable: true, Syncing: true, CurrentBlock: 500, HighestBlock: 1000, Peers: new(uint64(10))}
	elDown := &ExecutionStatus{Error: "port-forward: pod is not running"}
	clSynced := &ConsensusStatus{Reachable: true, HeadSlot: 1750123, Peers: new(uint64(80))}
	clDown := &ConsensusStatus{Error: "connection refused"}

	cases := []struct {
		name        string
		pods        []PodStatus
		el          *ExecutionStatus
		cl          *ConsensusStatus
		state       string
		summary     string
		pct         float64 // -1 = no progress
		warningPart string
	}{
		{"synced", running, elSynced, clSynced, VerdictSynced, "synced — head slot 1750123, block 21000000", 100, ""},
		{"cl syncing uses EL progress", running, elSyncing, &ConsensusStatus{Reachable: true, HeadSlot: 1000, SyncDistance: 300, IsSyncing: true},
			VerdictSyncing, "beacon syncing, 300 slots (~1h00m) behind (head slot 1000); execution ~50.0%", 50, ""},
		{"cl syncing without EL progress", running, elSynced, &ConsensusStatus{Reachable: true, HeadSlot: 900, SyncDistance: 100, IsSyncing: true},
			VerdictSyncing, "beacon syncing, 100 slots (~20m) behind", 90, ""},
		{"optimistic", running, elSyncing, &ConsensusStatus{Reachable: true, HeadSlot: 5, IsOptimistic: true},
			VerdictSyncing, "beacon at head, execution still syncing (optimistic) ~50.0%", 50, ""},
		{"el offline", running, elDown, &ConsensusStatus{Reachable: true, ELOffline: true, IsSyncing: true},
			VerdictELOffline, "EL offline", -1, ""},
		{"cl synced, el rpc unreachable", running, elDown, clSynced, VerdictDegraded, "execution JSON-RPC is unreachable", -1, ""},
		{"cl synced, el syncing", running, elSyncing, clSynced, VerdictSyncing, "beacon at head, execution syncing ~50.0%", 50, ""},
		{"both down, no pods", []PodStatus{}, elDown, clDown, VerdictNotDeployed, "no node pods", -1, ""},
		{"both down, crashlooping", []PodStatus{{Name: "ethereum-beacon-0", Component: "beacon", Phase: "Running", Reason: "CrashLoopBackOff", Restarts: 4}}, elDown, clDown,
			VerdictUnreachable, "beacon pod CrashLoopBackOff", -1, "beacon pod ethereum-beacon-0 not ready (CrashLoopBackOff), 4 restarts"},
		{"cl down, el waiting at genesis", running, &ExecutionStatus{Reachable: true}, clDown, VerdictWaiting, "waiting for the beacon node", -1, ""},
		{"cl down, el syncing", running, elSyncing, clDown, VerdictSyncing, "execution syncing ~50.0%; beacon API unreachable", 50, ""},
		{"cl down, el has head", running, elSynced, clDown, VerdictDegraded, "beacon API unreachable; execution at block 21000000", -1, ""},
		{"zero peers warns", running, &ExecutionStatus{Reachable: true, HeadBlock: 1, Peers: new(uint64(0))}, clSynced, VerdictSynced, "synced", 100, "execution client has 0 peers"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := ComputeVerdict(c.pods, c.el, c.cl)
			if v.State != c.state {
				t.Fatalf("state = %q (%s), want %q", v.State, v.Summary, c.state)
			}
			if !strings.Contains(v.Summary, c.summary) {
				t.Fatalf("summary = %q, want it to contain %q", v.Summary, c.summary)
			}
			switch {
			case c.pct < 0 && v.ProgressPct != nil:
				t.Fatalf("progress = %v, want none", *v.ProgressPct)
			case c.pct >= 0 && (v.ProgressPct == nil || *v.ProgressPct != c.pct):
				t.Fatalf("progress = %v, want %v", v.ProgressPct, c.pct)
			}
			if c.warningPart != "" && !strings.Contains(strings.Join(v.Warnings, "|"), c.warningPart) {
				t.Fatalf("warnings = %v, want one containing %q", v.Warnings, c.warningPart)
			}
		})
	}
}

// fakeEL serves the JSON-RPC methods the prober calls, requiring the
// Content-Type header geth/reth insist on.
func fakeEL(t *testing.T, syncingFixture string) *httptest.Server {
	t.Helper()

	results := map[string]string{
		"eth_syncing":        string(rpcResult(t, syncingFixture)),
		"eth_blockNumber":    `"0x1406f40"`,
		"net_peerCount":      `"0x2a"`,
		"web3_clientVersion": `"reth/v2.7.0-abcdef/x86_64-unknown-linux-gnu"`,
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "unsupported", http.StatusUnsupportedMediaType)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(body, &req)
		res, ok := results[req.Method]
		if !ok {
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":` + res + `}`))
	}))
}

func fakeBeacon(t *testing.T, syncingFixture string) *httptest.Server {
	t.Helper()

	routes := map[string][]byte{
		"/eth/v1/node/syncing":    fixture(t, syncingFixture),
		"/eth/v1/node/peer_count": fixture(t, "beacon_peer_count.json"),
		"/eth/v1/node/version":    fixture(t, "beacon_version.json"),
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
}

func TestNodeProber_Probe(t *testing.T) {
	el := fakeEL(t, "reth_syncing.json")
	defer el.Close()
	cl := fakeBeacon(t, "beacon_optimistic.json")
	defer cl.Close()

	var (
		mu        sync.Mutex
		forwarded []string
	)
	prober := &NodeProber{
		Forward: func(_ context.Context, ns, svc string, port int) (string, func(), error) {
			mu.Lock()
			forwarded = append(forwarded, ns+"/"+svc)
			mu.Unlock()
			switch svc {
			case executionService:
				return el.URL, func() {}, nil
			case beaconService:
				return cl.URL, func() {}, nil
			}
			return "", nil, errors.New("unknown service")
		},
		Pods: func(context.Context, string) ([]PodStatus, error) {
			return parsePodList(fixture(t, "pods.json"))
		},
		HTTP: &http.Client{Timeout: 2 * time.Second},
	}

	d := Deployment{Type: "ethereum", ID: "hoodi", Namespace: "ethereum-hoodi", Network: "hoodi"}
	st := prober.Probe(context.Background(), d)

	if st.Execution == nil || !st.Execution.Reachable || st.Execution.HeadBlock != 0x1406f40 ||
		st.Execution.Peers == nil || *st.Execution.Peers != 42 || !strings.HasPrefix(st.Execution.ClientVersion, "reth/") {
		t.Fatalf("execution = %+v", st.Execution)
	}
	if st.Consensus == nil || !st.Consensus.IsOptimistic || st.Consensus.Peers == nil || *st.Consensus.Peers != 56 ||
		!strings.HasPrefix(st.Consensus.Version, "Lighthouse/") {
		t.Fatalf("consensus = %+v", st.Consensus)
	}
	if st.Verdict.State != VerdictSyncing || len(st.Pods) != 3 {
		t.Fatalf("verdict = %+v, pods = %d", st.Verdict, len(st.Pods))
	}
	if len(forwarded) != 2 {
		t.Fatalf("forwarded = %v, want EL and CL", forwarded)
	}

	// JSON shape: deployment fields inline, empty pods as [] not null.
	raw, err := json.Marshal(prober.Probe(context.Background(), Deployment{Type: "aztec", ID: "x", Namespace: "aztec-x"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"aztec"`, `"pods":[]`, `"state":"not_supported"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("aztec status JSON %s missing %s", raw, want)
		}
	}
}

func TestNodeProber_ForwardFailureDegrades(t *testing.T) {
	prober := &NodeProber{
		Forward: func(context.Context, string, string, int) (string, func(), error) {
			return "", nil, errors.New("port-forward ethereum-x/svc/ethereum-beacon: error: unable to forward port because pod is not running")
		},
		Pods: func(context.Context, string) ([]PodStatus, error) {
			return nil, errors.New("namespaces \"ethereum-x\" not found")
		},
		HTTP: &http.Client{Timeout: time.Second},
	}

	st := prober.Probe(context.Background(), Deployment{Type: "ethereum", ID: "x", Namespace: "ethereum-x"})
	if st.Verdict.State != VerdictNotDeployed || st.PodsError == "" || st.Execution.Reachable || st.Consensus.Reachable {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(st.Consensus.Error, "pod is not running") {
		t.Fatalf("consensus error lost kubectl detail: %q", st.Consensus.Error)
	}
}

// A pod restarting behind the port-forward surfaces as EOF on the ephemeral
// 127.0.0.1:<port> URL; neither the URL nor the bare EOF should reach the user.
func TestProbeBeaconAPI_DroppedConnectionHidesForwardURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}

		conn.Close()
	}))
	defer srv.Close()

	cl := &ConsensusStatus{}
	ProbeBeaconAPI(context.Background(), srv.Client(), srv.URL, cl)

	if cl.Reachable {
		t.Fatal("reachable = true for a dropped connection")
	}

	if strings.Contains(cl.Error, "127.0.0.1") || !strings.Contains(cl.Error, "not answering") {
		t.Fatalf("error = %q, want a plain 'not answering' without the forward URL", cl.Error)
	}
}

func TestPodWarnings_OOMKilledNamesTheCause(t *testing.T) {
	got := podWarnings([]PodStatus{{Name: "ethereum-beacon-0", Component: "beacon", Phase: "Running", Reason: "OOMKilled", Restarts: 9}})
	if len(got) != 1 || !strings.Contains(got[0], "out of memory") || !strings.Contains(got[0], "9 restarts") {
		t.Fatalf("podWarnings = %q", got)
	}
}
