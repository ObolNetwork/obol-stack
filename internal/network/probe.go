package network

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// UpstreamProbeResult records the outcome of a quick eth_chainId probe against
// an eRPC upstream. The check exists so `obol network status` can warn about
// custom pins (most often `obol network add <name> --endpoint <local-anvil>`
// left over from an integration flow) that no longer reach a live node.
type UpstreamProbeResult struct {
	ID            string
	Endpoint      string
	DeclaredChain int
	ObservedChain int
	Reachable     bool
	Err           string
}

// Mismatch returns true when the upstream answered eth_chainId with a chain id
// that does not match the chain id declared in the eRPC config. A mismatch is
// almost always a stale custom pin re-pointing at a different fork.
func (r UpstreamProbeResult) Mismatch() bool {
	return r.Reachable && r.ObservedChain != 0 && r.ObservedChain != r.DeclaredChain
}

// ProbeUpstream sends a single eth_chainId JSON-RPC call to the given upstream
// with a bounded timeout. It never panics and always returns a result; the
// caller decides how to render warnings.
func ProbeUpstream(ctx context.Context, info RPCUpstreamInfo, timeout time.Duration) UpstreamProbeResult {
	res := UpstreamProbeResult{
		ID:            info.ID,
		Endpoint:      info.Endpoint,
		DeclaredChain: info.ChainID,
	}

	if strings.TrimSpace(info.Endpoint) == "" {
		res.Err = "empty endpoint"
		return res
	}

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`)

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodPost, info.Endpoint, bytes.NewReader(body))
	if err != nil {
		res.Err = err.Error()
		return res
	}
	req.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		res.Err = fmt.Sprintf("http %d", resp.StatusCode)
		return res
	}

	var payload struct {
		Result string `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		res.Err = "decode: " + err.Error()
		return res
	}
	if payload.Error != nil {
		res.Err = payload.Error.Message
		return res
	}

	chainID, err := parseHexUint(payload.Result)
	if err != nil {
		res.Err = "parse chainId: " + err.Error()
		return res
	}

	res.Reachable = true
	res.ObservedChain = chainID
	return res
}

// ProbeAllUpstreams probes every upstream listed in the eRPC config in parallel
// with a per-probe timeout. The returned slice is in the same order as
// ListRPCNetworks output (chain-grouped), suitable for direct rendering.
func ProbeAllUpstreams(ctx context.Context, cfg *config.Config, timeout time.Duration) ([]UpstreamProbeResult, error) {
	networks, err := ListRPCNetworks(cfg)
	if err != nil {
		return nil, err
	}

	var flat []RPCUpstreamInfo
	for _, n := range networks {
		flat = append(flat, n.Upstreams...)
	}

	results := make([]UpstreamProbeResult, len(flat))

	var wg sync.WaitGroup
	for i, info := range flat {
		wg.Add(1)
		go func(i int, info RPCUpstreamInfo) {
			defer wg.Done()
			results[i] = ProbeUpstream(ctx, info, timeout)
		}(i, info)
	}
	wg.Wait()

	return results, nil
}

// parseHexUint accepts a hex string with or without a 0x prefix and returns the
// parsed integer. We keep it small and unsigned because chain ids fit in int.
func parseHexUint(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	v, err := strconv.ParseInt(s, 16, 64)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}
