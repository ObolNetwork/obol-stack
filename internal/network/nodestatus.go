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
	"os"
	"os/exec"
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

	// slotSeconds is the post-merge slot time, for "N slots behind" → time.
	slotSeconds = 12
)

// Verdict states, from best to worst. JSON consumers key on these.
const (
	VerdictSynced       = "synced"
	VerdictSyncing      = "syncing"
	VerdictWaiting      = "waiting"
	VerdictDegraded     = "degraded"
	VerdictELOffline    = "el_offline"
	VerdictUnreachable  = "unreachable"
	VerdictNotDeployed  = "not_deployed"
	VerdictNotProbed    = "not_probed"
	VerdictNotSupported = "not_supported"
)

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
}

// PodStatus is a pod in the network namespace, condensed.
type PodStatus struct {
	Name      string `json:"name"`
	Component string `json:"component"`
	Phase     string `json:"phase"`
	Ready     bool   `json:"ready"`
	Restarts  int32  `json:"restarts"`
	Reason    string `json:"reason,omitempty"`
}

// Verdict is the one-line answer to "is my node usable yet?".
type Verdict struct {
	State       string   `json:"state"`
	Summary     string   `json:"summary"`
	ProgressPct *float64 `json:"progress_pct,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// NodeStatus is the live state of one installed deployment.
type NodeStatus struct {
	Deployment

	Pods      []PodStatus      `json:"pods"`
	PodsError string           `json:"pods_error,omitempty"`
	Execution *ExecutionStatus `json:"execution,omitempty"`
	Consensus *ConsensusStatus `json:"consensus,omitempty"`
	Verdict   Verdict          `json:"verdict"`
}

// NodeProber reaches in-cluster node APIs. Production wiring
// (NewKubectlNodeProber) uses short-lived `kubectl port-forward`s on obol's
// own kubeconfig; tests swap Forward/Pods for httptest servers and fixtures.
type NodeProber struct {
	// Forward opens a tunnel to namespace/service:port and returns its base
	// URL and a closer.
	Forward func(ctx context.Context, namespace, service string, port int) (string, func(), error)
	// Pods lists the pods in a namespace.
	Pods func(ctx context.Context, namespace string) ([]PodStatus, error)
	// HTTP is used for every API call; its Timeout bounds each request.
	HTTP *http.Client
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
		HTTP: &http.Client{Timeout: 3 * time.Second},
	}
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

// Probe gathers pods + EL + CL status for one deployment and
// computes its verdict. Every failure degrades into the result; it never
// returns an error.
func (p *NodeProber) Probe(ctx context.Context, d Deployment) NodeStatus {
	st := NodeStatus{Deployment: d, Pods: []PodStatus{}}

	if d.Type != "ethereum" {
		st.Verdict = Verdict{State: VerdictNotSupported, Summary: fmt.Sprintf("live status not implemented for %s; see obol kubectl get pods -n %s", d.Type, d.Namespace)}
		return st
	}

	if d.Error != "" {
		st.Verdict = Verdict{State: VerdictNotProbed, Summary: d.Error}
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
	wg.Wait()

	st.Verdict = ComputeVerdict(st.Pods, st.Execution, st.Consensus)

	return st
}

func (p *NodeProber) probeExecution(ctx context.Context, namespace string) *ExecutionStatus {
	el := &ExecutionStatus{}

	base, closeFn, err := p.Forward(ctx, namespace, executionService, executionRPCPort)
	if err != nil {
		el.Error = err.Error()
		return el
	}
	defer closeFn()

	ProbeExecutionAPI(ctx, p.HTTP, base, el)

	return el
}

func (p *NodeProber) probeConsensus(ctx context.Context, namespace string) *ConsensusStatus {
	cl := &ConsensusStatus{}

	base, closeFn, err := p.Forward(ctx, namespace, beaconService, beaconAPIPort)
	if err != nil {
		cl.Error = err.Error()
		return cl
	}
	defer closeFn()

	ProbeBeaconAPI(ctx, p.HTTP, base, cl)

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
// the reachability gate; peer count and version are best-effort.
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
}

// parseEthSyncing handles eth_syncing's two shapes: false, or an object
// with hex startingBlock/currentBlock/highestBlock (extra fields such as
// reth's "stages" are ignored).
func parseEthSyncing(raw json.RawMessage, el *ExecutionStatus) error {
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		el.Syncing = b
		return nil
	}

	var obj struct {
		CurrentBlock string `json:"currentBlock"`
		HighestBlock string `json:"highestBlock"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("eth_syncing: unexpected result %s", truncate(string(raw), 80))
	}

	el.Syncing = true
	el.CurrentBlock, _ = parseHex(obj.CurrentBlock)
	el.HighestBlock, _ = parseHex(obj.HighestBlock)

	return nil
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
// head is optimistic (EL still catching up).
func ComputeVerdict(pods []PodStatus, el *ExecutionStatus, cl *ConsensusStatus) Verdict {
	v := Verdict{Warnings: podWarnings(pods)}

	elUp := el != nil && el.Reachable
	clUp := cl != nil && cl.Reachable

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
	case clUp:
		v.State = VerdictSynced
		v.Summary = fmt.Sprintf("synced — head slot %d, block %d", cl.HeadSlot, el.HeadBlock)
		pct := 100.0
		v.ProgressPct = &pct
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

func elProgress(el *ExecutionStatus) (float64, bool) {
	if el == nil || !el.Reachable || !el.Syncing || el.HighestBlock == 0 {
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

func podWarnings(pods []PodStatus) []string {
	var out []string

	for _, p := range pods {
		if !p.Ready && (p.Reason != "" || p.Restarts > 0) {
			msg := fmt.Sprintf("%s pod %s not ready", p.Component, p.Name)
			switch p.Reason {
			case "":
			case "OOMKilled":
				msg += " (OOMKilled: out of memory — check host memory and the pod's limits)"
			default:
				msg += " (" + p.Reason + ")"
			}
			if p.Restarts > 0 {
				msg += fmt.Sprintf(", %d restarts", p.Restarts)
			}
			out = append(out, msg)
		}
	}

	return out
}

func firstUnhealthyPod(pods []PodStatus) string {
	for _, p := range pods {
		if p.Ready {
			continue
		}

		reason := p.Reason
		if reason == "" {
			reason = p.Phase
		}

		return fmt.Sprintf("%s pod %s", p.Component, reason)
	}

	return ""
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

// kubectlPods lists pods in namespace via obol's kubeconfig.
func kubectlPods(ctx context.Context, bin, kubeconfig, namespace string) ([]PodStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "get", "pods", "-n", namespace, "-o", "json", "--request-timeout=4s")
	cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, errors.New(msg)
		}

		return nil, err
	}

	return parsePodList(out)
}

// parsePodList condenses `kubectl get pods -o json`.
func parsePodList(raw []byte) ([]PodStatus, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool  `json:"ready"`
					RestartCount int32 `json:"restartCount"`
					State        struct {
						Waiting *struct {
							Reason string `json:"reason"`
						} `json:"waiting"`
						Terminated *struct {
							Reason string `json:"reason"`
						} `json:"terminated"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse pod list: %w", err)
	}

	pods := make([]PodStatus, 0, len(list.Items))
	for _, it := range list.Items {
		p := PodStatus{
			Name:      it.Metadata.Name,
			Component: it.Metadata.Labels["app.kubernetes.io/name"],
			Phase:     it.Status.Phase,
			Ready:     len(it.Status.ContainerStatuses) > 0,
		}
		if p.Component == "" {
			p.Component = p.Name
		}

		for _, cs := range it.Status.ContainerStatuses {
			p.Ready = p.Ready && cs.Ready
			p.Restarts += cs.RestartCount

			if p.Reason == "" && cs.State.Waiting != nil {
				p.Reason = cs.State.Waiting.Reason
			}

			if p.Reason == "" && cs.State.Terminated != nil {
				p.Reason = cs.State.Terminated.Reason
			}
		}

		pods = append(pods, p)
	}

	return pods, nil
}
