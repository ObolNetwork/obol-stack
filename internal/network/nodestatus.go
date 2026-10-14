package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/kubectl"
)

// In-namespace Services rendered by the ethpandaops ethereum-node chart
// (release "ethereum", nameOverride execution/beacon) and their API ports.
const (
	executionService = "ethereum-execution"
	executionRPCPort = 8545
	beaconService    = "ethereum-beacon"
	beaconAPIPort    = 5052

	// Pod component labels (app.kubernetes.io/name) of the EL and CL pods.
	componentExecution = "execution"
	componentBeacon    = "beacon"

	// slotSeconds is the post-merge slot time, for "N slots behind" → time.
	slotSeconds = 12
)

// Verdict states, from best to worst. JSON consumers key on these.
const (
	VerdictSynced       = "synced"
	VerdictSyncing      = "syncing"
	VerdictWaiting      = "waiting"
	VerdictStopped      = "stopped"
	VerdictDegraded     = "degraded"
	VerdictELOffline    = "el_offline"
	VerdictUnreachable  = "unreachable"
	VerdictNotDeployed  = "not_deployed"
	VerdictNotProbed    = "not_probed"
	VerdictNotSupported = "not_supported"
)

// VerdictUnhealthy reports whether a verdict state means the node needs
// attention (`network status` exits 3 for these). A deliberately stopped
// node, or one that is still syncing, is not unhealthy.
func VerdictUnhealthy(state string) bool {
	switch state {
	case VerdictDegraded, VerdictELOffline, VerdictUnreachable, VerdictNotDeployed:
		return true
	default:
		return false
	}
}

// ExecutionStatus is what the EL's JSON-RPC says about itself.
type ExecutionStatus struct {
	Reachable     bool    `json:"reachable"`
	Error         string  `json:"error,omitempty"`
	ClientVersion string  `json:"client_version,omitempty"`
	Syncing       bool    `json:"syncing"`
	HeadBlock     uint64  `json:"head_block"`
	CurrentBlock  uint64  `json:"current_block,omitempty"`
	HighestBlock  uint64  `json:"highest_block,omitempty"`
	Peers         *uint64 `json:"peers,omitempty"`
	// Stages is reth's staged-sync pipeline from eth_syncing, in pipeline
	// order, annotated with done/current/pending against the sync target.
	Stages []SyncStage `json:"stages,omitempty"`
}

// ConsensusStatus is what the beacon node API says about itself.
type ConsensusStatus struct {
	Reachable    bool    `json:"reachable"`
	Error        string  `json:"error,omitempty"`
	Version      string  `json:"version,omitempty"`
	HeadSlot     uint64  `json:"head_slot"`
	SyncDistance uint64  `json:"sync_distance"`
	IsSyncing    bool    `json:"is_syncing"`
	IsOptimistic bool    `json:"is_optimistic"`
	ELOffline    bool    `json:"el_offline"`
	Peers        *uint64 `json:"peers,omitempty"`
	// SyncState is Lighthouse's own sync state (/lighthouse/syncing):
	// SyncingFinalized, SyncingHead, BackFillSyncing, SyncTransition,
	// Synced or Stalled. Empty for other clients.
	SyncState         string `json:"sync_state,omitempty"`
	SyncTargetSlot    uint64 `json:"sync_target_slot,omitempty"`
	BackfillCompleted uint64 `json:"backfill_completed,omitempty"`
	BackfillRemaining uint64 `json:"backfill_remaining,omitempty"`
	// Stages is the human view of the sync (finalized → head → backfill).
	Stages []SyncStage `json:"stages,omitempty"`
}

// Verdict is the one-line answer to "is my node usable yet?".
type Verdict struct {
	State       string   `json:"state"`
	Summary     string   `json:"summary"`
	ProgressPct *float64 `json:"progress_pct,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// NodeERPC links a node to the RPC gateway.
type NodeERPC struct {
	// Upstream is the eRPC upstream id the node registers as.
	Upstream string `json:"upstream"`
	// Registered is whether the live eRPC config lists that upstream.
	Registered bool `json:"registered"`
	// Serving is the upstream that answered a probe request for the node's
	// chain through eRPC (empty when not probed).
	Serving string `json:"serving,omitempty"`
}

// NodeStatus is the live state of one installed deployment.
type NodeStatus struct {
	Deployment

	Pods      []PodStatus      `json:"pods"`
	PodsError string           `json:"pods_error,omitempty"`
	Problems  []PodProblem     `json:"problems"`
	Execution *ExecutionStatus `json:"execution,omitempty"`
	Consensus *ConsensusStatus `json:"consensus,omitempty"`
	Storage   *StorageStatus   `json:"storage,omitempty"`
	ERPC      *NodeERPC        `json:"erpc,omitempty"`
	// GrafanaURL is the node's dashboard on the local Grafana route.
	GrafanaURL string  `json:"grafana_url,omitempty"`
	Verdict    Verdict `json:"verdict"`
}

// GrafanaNodeURL is the obol-eth-node dashboard filtered to one namespace.
func GrafanaNodeURL(namespace string) string {
	return "http://obol.stack:8080/grafana/d/obol-eth-node?var-namespace=" + url.QueryEscape(namespace)
}

// LocalUpstreamID is the eRPC upstream id RegisterERPCUpstream uses for a
// local deployment.
func LocalUpstreamID(networkType, id string) string {
	return fmt.Sprintf("local-%s-%s", networkType, id)
}

// NodeProber reaches in-cluster node APIs. Production wiring
// (NewKubectlNodeProber) uses `kubectl port-forward`s on obol's own
// kubeconfig; tests swap the funcs for httptest servers and fixtures.
//
// By default every probe opens and closes its own forwards. After
// StartSession, forwards are kept open and reused across probes (for
// `network status --watch`) until Close; a forward whose API stopped
// answering is dropped and reopened on the next probe.
type NodeProber struct {
	// Forward opens a tunnel to namespace/service:port and returns its base
	// URL and a closer.
	Forward func(ctx context.Context, namespace, service string, port int) (string, func(), error)
	// Pods lists the pods in a namespace.
	Pods func(ctx context.Context, namespace string) ([]PodStatus, error)
	// ERPCConfig returns the parsed live eRPC config (erpc-config ConfigMap).
	ERPCConfig func(ctx context.Context) (map[string]any, error)
	// DiskUsage sums the bytes under dir (partial when it could not read
	// everything in time). Nil disables the Data line.
	DiskUsage func(ctx context.Context, dir string) (bytes uint64, partial bool, err error)
	// FreeBytes reports free space on the filesystem holding path.
	FreeBytes func(path string) (uint64, error)
	// DataDir is the host directory holding PVC data (<ns>/<pvc>).
	DataDir string
	// HTTP is used for every API call; its Timeout bounds each request.
	HTTP *http.Client
	// Now is the clock for "last 3m ago" (defaults to time.Now).
	Now func() time.Time

	pool    *forwardPool
	storeMu sync.Mutex
	stores  map[string]storageCacheEntry
}

// NewKubectlNodeProber returns a prober that reaches the cluster through
// obol's stack kubeconfig only (never the user's ambient KUBECONFIG).
func NewKubectlNodeProber(cfg *config.Config) *NodeProber {
	bin, kubeconfig := kubectl.Paths(cfg)

	return &NodeProber{
		Forward: func(ctx context.Context, namespace, service string, port int) (string, func(), error) {
			fwd, err := kubectl.PortForward(ctx, bin, kubeconfig, namespace, "svc/"+service, port, 5*time.Second)
			if err != nil {
				return "", nil, err
			}

			return fwd.URL(), fwd.Close, nil
		},
		Pods: func(ctx context.Context, namespace string) ([]PodStatus, error) {
			return kubectlPods(ctx, bin, kubeconfig, namespace)
		},
		// The caller checks the cluster once (kubectl.EnsureCluster).
		ERPCConfig: func(context.Context) (map[string]any, error) {
			return fetchERPCConfig(cfg)
		},
		DiskUsage: dirUsage,
		FreeBytes: freeDiskBytes,
		DataDir:   cfg.DataDir,
		HTTP:      &http.Client{Timeout: 3 * time.Second},
	}
}

func (p *NodeProber) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}

	return time.Now()
}

// StartSession keeps port-forwards open across probes until Close. ctx
// bounds the forwards' lifetime (cancel it, or call Close, to stop them).
func (p *NodeProber) StartSession(ctx context.Context) {
	p.pool = newForwardPool(ctx, p.Forward)
}

// Close closes every forward a session kept open. Safe without a session.
func (p *NodeProber) Close() {
	if p.pool != nil {
		p.pool.closeAll()
	}
}

// open returns a base URL for namespace/service:port. release(ok) must be
// called when done; ok=false means the API did not answer, so a session
// drops the forward (the pod behind it may have been replaced).
func (p *NodeProber) open(ctx context.Context, namespace, service string, port int) (string, func(ok bool), error) {
	if p.pool != nil {
		return p.pool.get(namespace, service, port)
	}

	base, closeFn, err := p.Forward(ctx, namespace, service, port)
	if err != nil {
		return "", nil, err
	}

	return base, func(bool) { closeFn() }, nil
}

// ProbeAll probes every deployment concurrently. Non-ethereum deployments
// are returned with VerdictNotSupported (no live probes for them yet).
func (p *NodeProber) ProbeAll(ctx context.Context, deployments []Deployment) []NodeStatus {
	out := make([]NodeStatus, len(deployments))

	var wg sync.WaitGroup
	for i, d := range deployments {
		wg.Go(func() { out[i] = p.Probe(ctx, d) })
	}
	wg.Wait()

	return out
}

// Unprobed is the status of a deployment that is not probed live
// (--no-probe): stopped nodes still say so.
func Unprobed(d Deployment, summary string) NodeStatus {
	st := NodeStatus{Deployment: d, Pods: []PodStatus{}, Problems: []PodProblem{}}
	if d.Type == TypeEthereum {
		st.GrafanaURL = GrafanaNodeURL(d.Namespace)
	}

	if d.Stopped {
		st.Verdict = Verdict{State: VerdictStopped, Summary: "stopped (data kept)"}
		return st
	}

	st.Verdict = Verdict{State: VerdictNotProbed, Summary: summary}

	return st
}

// Probe gathers pods + EL + CL status for one deployment and
// computes its verdict. Every failure degrades into the result; it never
// returns an error.
func (p *NodeProber) Probe(ctx context.Context, d Deployment) NodeStatus {
	st := NodeStatus{Deployment: d, Pods: []PodStatus{}, Problems: []PodProblem{}}

	if d.Type != TypeEthereum {
		st.Verdict = Verdict{State: VerdictNotSupported, Summary: fmt.Sprintf("live status not implemented for %s; see obol kubectl get pods -n %s", d.Type, d.Namespace)}
		return st
	}

	st.GrafanaURL = GrafanaNodeURL(d.Namespace)

	if d.Error != "" {
		st.Verdict = Verdict{State: VerdictNotProbed, Summary: d.Error}
		return st
	}

	if d.Stopped {
		st.Storage = p.storage(ctx, d)
		st.Verdict = Verdict{State: VerdictStopped, Summary: "stopped (data kept)"}

		return st
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		pods, err := p.Pods(ctx, d.Namespace)
		if err != nil {
			st.PodsError = err.Error()
			return
		}
		st.Pods = pods
	})
	wg.Go(func() { st.Execution = p.probeExecution(ctx, d.Namespace) })
	wg.Go(func() { st.Consensus = p.probeConsensus(ctx, d.Namespace) })
	wg.Go(func() { st.Storage = p.storage(ctx, d) })
	wg.Wait()

	st.Problems = PodProblems(d, st.Pods, p.now())
	st.Verdict = ComputeVerdict(st.Pods, st.Execution, st.Consensus)

	return st
}

func (p *NodeProber) probeExecution(ctx context.Context, namespace string) *ExecutionStatus {
	el := &ExecutionStatus{}

	base, release, err := p.open(ctx, namespace, executionService, executionRPCPort)
	if err != nil {
		el.Error = err.Error()
		return el
	}

	ProbeExecutionAPI(ctx, p.HTTP, base, el)
	release(el.Reachable)

	return el
}

func (p *NodeProber) probeConsensus(ctx context.Context, namespace string) *ConsensusStatus {
	cl := &ConsensusStatus{}

	base, release, err := p.open(ctx, namespace, beaconService, beaconAPIPort)
	if err != nil {
		cl.Error = err.Error()
		return cl
	}

	ProbeBeaconAPI(ctx, p.HTTP, base, cl)
	release(cl.Reachable)

	return cl
}

// ProbeExecutionAPI fills el from the EL JSON-RPC at base. eth_syncing is
// the reachability gate; the other calls are best-effort.
func ProbeExecutionAPI(ctx context.Context, client *http.Client, base string, el *ExecutionStatus) {
	syncing, err := jsonRPC(ctx, client, base, "eth_syncing")
	if err != nil {
		el.Error = err.Error()
		return
	}

	el.Reachable = true

	if err := parseEthSyncing(syncing, el); err != nil {
		el.Error = err.Error()
	}

	if raw, err := jsonRPC(ctx, client, base, "eth_blockNumber"); err == nil {
		if n, err := hexQuantity(raw); err == nil {
			el.HeadBlock = n
		}
	}

	if raw, err := jsonRPC(ctx, client, base, "net_peerCount"); err == nil {
		if n, err := hexQuantity(raw); err == nil {
			el.Peers = &n
		}
	}

	if raw, err := jsonRPC(ctx, client, base, "web3_clientVersion"); err == nil {
		_ = json.Unmarshal(raw, &el.ClientVersion)
	}
}

// ProbeBeaconAPI fills cl from the beacon node API at base. /node/syncing is
// the reachability gate; peer count, version and (for Lighthouse) its own
// sync state are best-effort.
func ProbeBeaconAPI(ctx context.Context, client *http.Client, base string, cl *ConsensusStatus) {
	code, body, err := httpGet(ctx, client, base+"/eth/v1/node/syncing")
	if err != nil {
		cl.Error = err.Error()
		return
	}

	if code != http.StatusOK {
		cl.Error = fmt.Sprintf("/eth/v1/node/syncing HTTP %d: %s", code, truncate(string(body), 120))
		return
	}

	cl.Reachable = true

	if err := parseBeaconSyncing(body, cl); err != nil {
		cl.Error = err.Error()
	}

	if code, body, err := httpGet(ctx, client, base+"/eth/v1/node/peer_count"); err == nil && code == http.StatusOK {
		if n, err := parseBeaconPeerCount(body); err == nil {
			cl.Peers = &n
		}
	}

	if code, body, err := httpGet(ctx, client, base+"/eth/v1/node/version"); err == nil && code == http.StatusOK {
		var v struct {
			Data struct {
				Version string `json:"version"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &v) == nil {
			cl.Version = v.Data.Version
		}
	}

	if strings.HasPrefix(strings.ToLower(cl.Version), "lighthouse") {
		if code, body, err := httpGet(ctx, client, base+"/lighthouse/syncing"); err == nil && code == http.StatusOK {
			_ = parseLighthouseSyncing(body, cl)
		}
	}

	cl.Stages = consensusStages(cl)
}

// parseEthSyncing handles eth_syncing's two shapes: false, or an object
// with hex startingBlock/currentBlock/highestBlock, plus reth's "stages".
func parseEthSyncing(raw json.RawMessage, el *ExecutionStatus) error {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		el.Syncing = b
		return nil
	}

	var obj struct {
		CurrentBlock string `json:"currentBlock"`
		HighestBlock string `json:"highestBlock"`
		Stages       []struct {
			Name  string          `json:"name"`
			Block json.RawMessage `json:"block"`
		} `json:"stages"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("eth_syncing: unexpected result %s", truncate(string(raw), 80))
	}

	el.Syncing = true
	el.CurrentBlock, _ = parseHex(obj.CurrentBlock)
	el.HighestBlock, _ = parseHex(obj.HighestBlock)

	el.Stages = nil
	for _, s := range obj.Stages {
		el.Stages = append(el.Stages, SyncStage{Name: s.Name, Block: flexQuantity(s.Block), Unit: UnitBlocks})
	}

	annotateExecutionStages(el)

	return nil
}

// flexQuantity reads a JSON-RPC quantity that may be a hex string, a
// decimal string or a bare number. Unparseable values read as 0.
func flexQuantity(raw json.RawMessage) uint64 {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
			n, _ := parseHex(s)
			return n
		}

		n, _ := strconv.ParseUint(s, 10, 64)

		return n
	}

	var n uint64
	_ = json.Unmarshal(raw, &n)

	return n
}

// beaconUint accepts the beacon API's quoted decimal integers (and bare
// numbers, which some clients have emitted).
type beaconUint uint64

func (u *beaconUint) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}

	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return err
	}

	*u = beaconUint(n)

	return nil
}

func parseBeaconSyncing(body []byte, cl *ConsensusStatus) error {
	var resp struct {
		Data struct {
			HeadSlot     beaconUint `json:"head_slot"`
			SyncDistance beaconUint `json:"sync_distance"`
			IsSyncing    bool       `json:"is_syncing"`
			IsOptimistic bool       `json:"is_optimistic"`
			ELOffline    bool       `json:"el_offline"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return fmt.Errorf("/eth/v1/node/syncing: %w", err)
	}

	cl.HeadSlot = uint64(resp.Data.HeadSlot)
	cl.SyncDistance = uint64(resp.Data.SyncDistance)
	cl.IsSyncing = resp.Data.IsSyncing
	cl.IsOptimistic = resp.Data.IsOptimistic
	cl.ELOffline = resp.Data.ELOffline

	return nil
}

func parseBeaconPeerCount(body []byte) (uint64, error) {
	var resp struct {
		Data struct {
			Connected beaconUint `json:"connected"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}

	return uint64(resp.Data.Connected), nil
}

// ComputeVerdict turns raw node state into one state + one-line summary.
// The consensus view wins where both are available: it knows whether the
// EL is reachable over the engine API (el_offline) and whether the chain
// head is optimistic (EL still catching up). A client whose API is down
// because its pod is crash-looping makes the node degraded, even while the
// other client still reports progress.
func ComputeVerdict(pods []PodStatus, el *ExecutionStatus, cl *ConsensusStatus) Verdict {
	v := Verdict{Warnings: podWarnings(pods)}

	elUp := el != nil && el.Reachable
	clUp := cl != nil && cl.Reachable
	elCrash := crashCause(pods, componentExecution)
	clCrash := crashCause(pods, componentBeacon)

	switch {
	case el == nil && cl == nil:
		v.State, v.Summary = VerdictNotProbed, "not probed"
	case !elUp && !clUp:
		if len(pods) == 0 {
			v.State, v.Summary = VerdictNotDeployed, "no node pods found (not synced to the cluster yet?)"
		} else {
			v.State, v.Summary = VerdictUnreachable, "execution and beacon APIs unreachable"
			if bad := firstUnhealthyPod(pods); bad != "" {
				v.Summary += " — " + bad
			}
		}
	case clUp && cl.ELOffline:
		v.State, v.Summary = VerdictELOffline, "EL offline: the beacon node cannot reach the execution client"
	case clUp && cl.IsSyncing:
		v.State = VerdictSyncing
		v.Summary = fmt.Sprintf("beacon syncing, %s behind (head slot %d)", slotsBehind(cl.SyncDistance), cl.HeadSlot)
		if pct, ok := elProgress(el); ok {
			v.ProgressPct = &pct
			v.Summary += fmt.Sprintf("; execution ~%.1f%%", pct)
		} else if cl.HeadSlot+cl.SyncDistance > 0 {
			pct := percent(cl.HeadSlot, cl.HeadSlot+cl.SyncDistance)
			v.ProgressPct = &pct
		}
	case clUp && cl.IsOptimistic:
		v.State = VerdictSyncing
		v.Summary = "beacon at head, execution still syncing (optimistic)"
		if pct, ok := elProgress(el); ok {
			v.ProgressPct = &pct
			v.Summary += fmt.Sprintf(" ~%.1f%%", pct)
		}
	case clUp && elUp && el.Syncing:
		v.State = VerdictSyncing
		v.Summary = "beacon at head, execution syncing"
		if pct, ok := elProgress(el); ok {
			v.ProgressPct = &pct
			v.Summary += fmt.Sprintf(" ~%.1f%%", pct)
		}
	case clUp && !elUp:
		v.State = VerdictDegraded
		v.Summary = fmt.Sprintf("beacon synced (slot %d) but the execution JSON-RPC is unreachable", cl.HeadSlot)
		if elCrash != "" {
			v.Summary = fmt.Sprintf("execution client restarting (%s); beacon at slot %d", elCrash, cl.HeadSlot)
		}
	case clUp:
		v.State = VerdictSynced
		v.Summary = fmt.Sprintf("synced — head slot %d, block %d", cl.HeadSlot, el.HeadBlock)
		pct := 100.0
		v.ProgressPct = &pct
	case clCrash != "":
		v.State = VerdictDegraded
		v.Summary = fmt.Sprintf("beacon node restarting (%s); the execution client cannot follow the chain without it", clCrash)
		if pct, ok := elProgress(el); ok {
			v.ProgressPct = &pct
		}
	case el.Syncing:
		v.State = VerdictSyncing
		v.Summary = "execution syncing; beacon API unreachable"
		if pct, ok := elProgress(el); ok {
			v.ProgressPct = &pct
			v.Summary = fmt.Sprintf("execution syncing ~%.1f%%; beacon API unreachable", pct)
		}
	case el.HeadBlock == 0:
		v.State, v.Summary = VerdictWaiting, "execution at genesis, waiting for the beacon node (beacon API unreachable)"
	default:
		v.State = VerdictDegraded
		v.Summary = fmt.Sprintf("beacon API unreachable; execution at block %d", el.HeadBlock)
	}

	if elUp && el.Peers != nil && *el.Peers == 0 {
		v.Warnings = append(v.Warnings, "execution client has 0 peers")
	}

	if clUp && cl.Peers != nil && *cl.Peers == 0 {
		v.Warnings = append(v.Warnings, "beacon node has 0 peers")
	}

	return v
}

// elProgress is the execution sync progress. With reth's staged sync it
// is the Execution stage against the sync target (current/highest stay 0
// for most of the pipeline); otherwise current/highest from eth_syncing.
func elProgress(el *ExecutionStatus) (float64, bool) {
	if el == nil || !el.Reachable || !el.Syncing {
		return 0, false
	}

	if st, target, ok := executionStage(el); ok && target > 0 {
		return percent(st.Block, target), true
	}

	if el.HighestBlock == 0 {
		return 0, false
	}

	return percent(el.CurrentBlock, el.HighestBlock), true
}

func percent(n, d uint64) float64 {
	if d == 0 {
		return 0
	}

	p := float64(n) / float64(d) * 100
	if p > 100 {
		p = 100
	}

	// One decimal place keeps JSON stable and readable.
	return float64(int(p*10)) / 10
}

// slotsBehind renders a sync distance as "N slots (~2h05m)".
func slotsBehind(distance uint64) string {
	d := time.Duration(distance) * slotSeconds * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%d slots (~%.1fd)", distance, d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%d slots (~%dh%02dm)", distance, int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%d slots (~%dm)", distance, int(d.Minutes()))
	}
}

// --- transport helpers ------------------------------------------------------

func jsonRPC(ctx context.Context, client *http.Client, base, method string) (json.RawMessage, error) {
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":[]}`, method)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, transportError(err))
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", method, resp.StatusCode, truncate(string(raw), 120))
	}

	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &rpc); err != nil {
		return nil, fmt.Errorf("%s: invalid JSON-RPC response: %w", method, err)
	}

	if rpc.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, rpc.Error.Message)
	}

	if len(rpc.Result) == 0 {
		return nil, fmt.Errorf("%s: empty result", method)
	}

	return rpc.Result, nil
}

func httpGet(ctx context.Context, client *http.Client, url string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, transportError(err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	return resp.StatusCode, body, nil
}

// transportError drops the request URL (an ephemeral port-forward address
// that means nothing to the user) and names the usual cause of a dropped
// connection: the pod behind the forward is starting or restarting.
func transportError(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		err = uerr.Err
	}

	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return errors.New("not answering (pod starting or restarting?)")
	}

	return err
}

func hexQuantity(raw json.RawMessage) (uint64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, err
	}

	return parseHex(s)
}

func parseHex(s string) (uint64, error) {
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if s == "" {
		return 0, errors.New("empty hex quantity")
	}

	return strconv.ParseUint(s, 16, 64)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}

	return s[:n] + "…"
}
