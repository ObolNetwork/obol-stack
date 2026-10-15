package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	erpcService  = "erpc"
	erpcHTTPPort = 80

	// ERPCEndpoint is the host-side eRPC route (hostname-restricted to
	// obol.stack); append /<network> (the chain alias).
	ERPCEndpoint = "http://obol.stack:8080/rpc"
)

// eRPC health states.
const (
	ERPCHealthy  = "healthy"
	ERPCDegraded = "degraded"
	ERPCDown     = "down"
	ERPCUnknown  = "unknown"
)

// ERPCPod is an eRPC gateway pod.
type ERPCPod struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Ready    bool   `json:"ready"`
	Restarts int    `json:"restarts"`
	Reason   string `json:"reason,omitempty"`
}

// ERPCChainCount is the v0.16 per-chain upstream count (kept for JSON
// consumers; Chains has the detail).
type ERPCChainCount struct {
	ChainID   int    `json:"chain_id"`
	Name      string `json:"name"`
	Upstreams int    `json:"upstreams"`
}

// ERPCUpstream is one configured upstream of a chain.
type ERPCUpstream struct {
	ID string `json:"id"`
	// Local marks an upstream that is a local node deployment
	// (local-<type>-<id>); Deployment is its "<type>/<id>".
	Local      bool   `json:"local"`
	Deployment string `json:"deployment,omitempty"`
}

// ERPCAttempt is one upstream eRPC tried for the probe request, from its
// X-Erpc-Upstreams header ("local-ethereum-mainnet=primary:empty").
type ERPCAttempt struct {
	Upstream string `json:"upstream"`
	Phase    string `json:"phase,omitempty"`
	Result   string `json:"result,omitempty"`
	Won      bool   `json:"won,omitempty"`
}

// ERPCChain is one chain eRPC routes, keyed by the same short network
// name as `obol network list` (the eRPC alias, else chain-<id>).
type ERPCChain struct {
	Network   string         `json:"network"`
	ChainID   int            `json:"chain_id"`
	Upstreams []ERPCUpstream `json:"upstreams"`
	// Serving is the upstream that answered one eth_blockNumber sent
	// through eRPC ("" when not probed or nobody answered).
	Serving    string        `json:"serving,omitempty"`
	FromCache  bool          `json:"from_cache,omitempty"`
	Attempts   []ERPCAttempt `json:"attempts,omitempty"`
	ProbeError string        `json:"probe_error,omitempty"`
}

// ERPCStatus is the RPC gateway's state.
type ERPCStatus struct {
	Endpoint  string           `json:"endpoint"`
	Health    string           `json:"health"`
	Pods      []ERPCPod        `json:"pods"`
	Upstreams []ERPCChainCount `json:"upstreams"`
	Chains    []ERPCChain      `json:"chains"`
	Error     string           `json:"error,omitempty"`
}

// StatusOptions tunes Gather.
type StatusOptions struct {
	// NoProbe skips live API calls (node APIs and the eRPC serving probe);
	// pods and the eRPC config are still read.
	NoProbe bool
	// Installed is every installed deployment, used to link eRPC's local
	// upstreams to them. Defaults to the probed deployments.
	Installed []Deployment
}

// StatusReport is `obol network status`: the gateway plus each node.
type StatusReport struct {
	Time  time.Time    `json:"time"`
	ERPC  ERPCStatus   `json:"erpc"`
	Nodes []NodeStatus `json:"nodes"`
}

// Unhealthy lists the nodes whose verdict needs attention.
func (r StatusReport) Unhealthy() []string {
	var out []string

	for _, n := range r.Nodes {
		if VerdictUnhealthy(n.Verdict.State) {
			out = append(out, n.Identifier())
		}
	}

	return out
}

// Gather builds the whole status report: eRPC (pods, config, which upstream
// serves each chain right now) and every deployment, concurrently. It never
// fails; problems are reported inside the result.
func (p *NodeProber) Gather(ctx context.Context, deployments []Deployment, opts StatusOptions) StatusReport {
	report := StatusReport{
		Time:  p.now().UTC(),
		ERPC:  ERPCStatus{Endpoint: ERPCEndpoint, Health: ERPCUnknown, Pods: []ERPCPod{}, Upstreams: []ERPCChainCount{}, Chains: []ERPCChain{}},
		Nodes: []NodeStatus{},
	}

	installed := opts.Installed
	if installed == nil {
		installed = deployments
	}

	var wg sync.WaitGroup

	wg.Go(func() { report.ERPC = p.gatherERPC(ctx, installed, opts.NoProbe) })
	wg.Go(func() {
		if opts.NoProbe {
			for _, d := range deployments {
				report.Nodes = append(report.Nodes, Unprobed(d, "not probed (--no-probe)"))
			}

			return
		}

		report.Nodes = p.ProbeAll(ctx, deployments)
	})
	wg.Wait()

	linkNodesToERPC(report.Nodes, report.ERPC.Chains)

	return report
}

func (p *NodeProber) gatherERPC(ctx context.Context, installed []Deployment, noProbe bool) ERPCStatus {
	st := ERPCStatus{Endpoint: ERPCEndpoint, Health: ERPCUnknown, Pods: []ERPCPod{}, Upstreams: []ERPCChainCount{}, Chains: []ERPCChain{}}

	var wg sync.WaitGroup

	wg.Go(func() {
		if p.Pods == nil {
			return
		}

		pods, err := p.Pods(ctx, erpcNamespace)
		if err != nil {
			return
		}

		st.Pods = erpcPods(pods)
		st.Health = erpcHealth(st.Pods)
	})

	var project string

	wg.Go(func() {
		if p.ERPCConfig == nil {
			return
		}

		cfg, err := p.ERPCConfig(ctx)
		if err != nil {
			st.Error = err.Error()
			return
		}

		project, st.Chains = ERPCChainsFromConfig(cfg, installed)
	})
	wg.Wait()

	if noProbe || len(st.Chains) == 0 || st.Health == ERPCDown {
		return st
	}

	base, release, err := p.open(ctx, erpcNamespace, erpcService, erpcHTTPPort)
	if err != nil {
		for i := range st.Chains {
			st.Chains[i].ProbeError = "eRPC unreachable: " + err.Error()
		}

		return st
	}

	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	// Upstreams may need a few seconds (failover, cold connections).
	slow := *client
	slow.Timeout = max(client.Timeout, 6*time.Second)

	var answered atomic.Bool

	for i := range st.Chains {
		wg.Go(func() {
			if probeERPCServing(ctx, &slow, base, project, &st.Chains[i]) {
				answered.Store(true)
			}
		})
	}
	wg.Wait()

	release(answered.Load())

	return st
}

func erpcPods(pods []PodStatus) []ERPCPod {
	out := []ERPCPod{}

	for _, p := range pods {
		if p.Component != "erpc" {
			continue
		}

		out = append(out, ERPCPod{Name: p.Name, Phase: p.Phase, Ready: p.Ready, Restarts: int(p.Restarts), Reason: p.Reason})
	}

	return out
}

func erpcHealth(pods []ERPCPod) string {
	if len(pods) == 0 {
		return ERPCDown
	}

	for _, p := range pods {
		if p.Ready {
			return ERPCHealthy
		}
	}

	return ERPCDegraded
}

// ERPCChainsFromConfig lists the chains in an eRPC config with their
// upstreams in priority order, sorted by chain id, and returns the
// project id that prefixes request paths. Upstreams named local-<type>-<id>
// are linked to the matching installed deployment.
func ERPCChainsFromConfig(erpcConfig map[string]any, installed []Deployment) (string, []ERPCChain) {
	chains := []ERPCChain{}

	project := erpcConfigProject(erpcConfig)
	if project == nil {
		return "", chains
	}

	projectID, _ := project["id"].(string)

	local := map[string]string{}
	for _, d := range installed {
		local[LocalUpstreamID(d.Type, d.ID)] = d.Identifier()
	}

	byChain := map[int]*ERPCChain{}
	get := func(chainID int) *ERPCChain {
		if c, ok := byChain[chainID]; ok {
			return c
		}

		c := &ERPCChain{ChainID: chainID, Network: fmt.Sprintf("chain-%d", chainID), Upstreams: []ERPCUpstream{}}
		byChain[chainID] = c

		return c
	}

	for _, n := range asMapSlice(project["networks"]) {
		evm, _ := n["evm"].(map[string]any)

		chainID := yamlInt(evm["chainId"])
		if chainID <= 0 {
			continue
		}

		if alias, _ := n["alias"].(string); alias != "" {
			get(chainID).Network = alias
		} else {
			get(chainID)
		}
	}

	for _, u := range asMapSlice(project["upstreams"]) {
		evm, _ := u["evm"].(map[string]any)

		chainID := yamlInt(evm["chainId"])
		if chainID <= 0 {
			continue
		}

		id, _ := u["id"].(string)
		up := ERPCUpstream{ID: id}

		if dep, ok := local[id]; ok {
			up.Local, up.Deployment = true, dep
		} else if strings.HasPrefix(id, "local-") {
			up.Local = true
			if typ, rest, ok := strings.Cut(strings.TrimPrefix(id, "local-"), "-"); ok {
				up.Deployment = typ + "/" + rest
			}
		}

		c := get(chainID)
		c.Upstreams = append(c.Upstreams, up)
	}

	for _, c := range byChain {
		chains = append(chains, *c)
	}

	sort.Slice(chains, func(i, j int) bool { return chains[i].ChainID < chains[j].ChainID })

	return projectID, chains
}

// probeERPCServing sends one eth_blockNumber for ch through eRPC and
// records which upstream answered (X-Erpc-Upstreams / X-Erpc-Upstream).
// It reports whether eRPC itself answered (an HTTP response, even an error).
func probeERPCServing(ctx context.Context, client *http.Client, base, project string, ch *ERPCChain) bool {
	if project == "" {
		project = "rpc"
	}

	path := fmt.Sprintf("/%s/evm/%d", project, ch.ChainID)
	if !strings.HasPrefix(ch.Network, "chain-") {
		path = "/" + project + "/" + ch.Network
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}`

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewBufferString(body))
	if err != nil {
		ch.ProbeError = err.Error()
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	// Ask for a live answer: a cached one doesn't say who serves now.
	req.Header.Set("X-Erpc-Skip-Cache-Read", "true")

	resp, err := client.Do(req)
	if err != nil {
		ch.ProbeError = transportError(err).Error()
		return false
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	ch.Attempts = ParseERPCUpstreamsHeader(resp.Header.Get("X-Erpc-Upstreams"))
	ch.Serving = servingUpstream(ch.Attempts, resp.Header.Get("X-Erpc-Upstream"))
	ch.FromCache = strings.EqualFold(resp.Header.Get("X-Erpc-Cache"), "HIT")

	var rpc struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	switch {
	case json.Unmarshal(raw, &rpc) != nil:
		ch.ProbeError = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, truncate(string(raw), 100))
	case rpc.Error != nil:
		ch.ProbeError = truncate(rpc.Error.Message, 160)
		if ch.Serving != "" && !attemptSucceeded(ch.Attempts, ch.Serving) {
			ch.Serving = ""
		}
	case resp.StatusCode != http.StatusOK:
		ch.ProbeError = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	return true
}

// ParseERPCUpstreamsHeader parses eRPC's X-Erpc-Upstreams header:
// "local-ethereum-mainnet=primary:empty;obol-rpc-mainnet=sweep:success:won".
func ParseERPCUpstreamsHeader(h string) []ERPCAttempt {
	var out []ERPCAttempt

	for part := range strings.SplitSeq(h, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		id, rest, _ := strings.Cut(part, "=")
		a := ERPCAttempt{Upstream: strings.TrimSpace(id)}

		fields := strings.Split(rest, ":")
		for i, f := range fields {
			f = strings.TrimSpace(f)
			switch {
			case f == "won":
				a.Won = true
			case i == 0 && len(fields) > 1:
				a.Phase = f
			case a.Result == "":
				a.Result = f
			}
		}

		out = append(out, a)
	}

	return out
}

// servingUpstream picks the upstream that answered: the attempt marked
// "won", else the single X-Erpc-Upstream header, else the last success.
func servingUpstream(attempts []ERPCAttempt, single string) string {
	for _, a := range attempts {
		if a.Won {
			return a.Upstream
		}
	}

	if single = strings.TrimSpace(single); single != "" {
		return single
	}

	for _, attempt := range slices.Backward(attempts) {
		if attempt.Result == "success" {
			return attempt.Upstream
		}
	}

	return ""
}

func attemptSucceeded(attempts []ERPCAttempt, upstream string) bool {
	for _, a := range attempts {
		if a.Upstream == upstream {
			return a.Won || a.Result == "success"
		}
	}

	// No per-attempt detail: trust the X-Erpc-Upstream header.
	return true
}

// linkNodesToERPC tells each ethereum node whether eRPC knows it and which
// upstream currently serves its chain.
func linkNodesToERPC(nodes []NodeStatus, chains []ERPCChain) {
	for i := range nodes {
		n := &nodes[i]
		if n.Type != TypeEthereum {
			continue
		}

		link := &NodeERPC{Upstream: LocalUpstreamID(n.Type, n.ID)}
		chainID := networkChainIDs[n.Network]

		for _, c := range chains {
			listed := false
			for _, u := range c.Upstreams {
				if u.ID == link.Upstream {
					listed = true
				}
			}

			if listed {
				link.Registered = true
			}

			if listed || (chainID != 0 && c.ChainID == chainID) {
				link.Serving = c.Serving
			}
		}

		n.ERPC = link
	}
}
