---
name: local-erpc
description: "Interact with the local eRPC instance in the obol-stack. Use this skill whenever you need to make Ethereum JSON-RPC calls through the stack's built-in RPC proxy, test RPC connectivity, check eRPC health or metrics, debug upstream issues, or understand the URL structure for reaching the eRPC gateway. Trigger on any mention of: eRPC, RPC calls, eth_blockNumber, eth_getBlock, eth_call, JSON-RPC, blockchain queries, RPC endpoint, RPC proxy, port-forward erpc, /rpc route, healthcheck erpc."
user-invokable: true
metadata:
  version: "1.0.0"
  domain: infrastructure
  triggers: erpc, rpc, json-rpc, eth_blockNumber, eth_call, rpc proxy, blockchain query, obol rpc, port-forward erpc
  role: specialist
  scope: rpc-interaction
  output-format: code-and-commands
  related-skills: obol-stack-dev
---

# local-erpc — Talk to the Obol Stack's eRPC Gateway

eRPC is the Ethereum JSON-RPC proxy deployed automatically with every obol-stack cluster.
It provides load balancing, retry logic, in-memory caching, and CORS support for all
blockchain RPC calls. Traffic from the host machine reaches it via Traefik at `obol.stack/rpc`.

## URL Reference

| Context | URL pattern | Notes |
|---------|-------------|-------|
| **Host machine** | `http://obol.stack/rpc/rpc/<arch>/<chainId>` | Requires `/etc/hosts`: `127.0.0.1 obol.stack` |
| **Host (alias)** | `http://obol.stack/rpc/rpc/<network-alias>` | e.g. `/rpc/rpc/mainnet` |
| **In-cluster** | `http://erpc.erpc.svc.cluster.local:4000/rpc/<network-alias>` | Used by other pods |
| **Port-forward** | `http://localhost:4000/rpc/<arch>/<chainId>` | After `obol kubectl port-forward` |
| **Health check** | `http://localhost:4000/healthcheck` | Requires port-forward to erpc:4000 |
| **Metrics** | `http://localhost:4001/metrics` | Requires port-forward to erpc:4001 |

**eRPC project ID in obol-stack**: `rpc`

**Supported networks** (configured when you ran `obol stack init`):

| Network | Alias | Chain ID | Full host URL |
|---------|-------|----------|---------------|
| Ethereum Mainnet | `mainnet` | `1` | `http://obol.stack/rpc/rpc/evm/1` |
| Hoodi Testnet | `hoodi` | `560048` | `http://obol.stack/rpc/rpc/evm/560048` |

> **Which network is active?** The stack deploys eRPC for one network at a time. Check:
> ```bash
> grep 'network:' ~/.config/obol/defaults/helmfile.yaml
> # or in development mode:
> grep 'network:' .workspace/config/defaults/helmfile.yaml
> ```

## Quick-Start: Common JSON-RPC Calls

```bash
# Get the current block number
curl http://obol.stack/rpc/rpc/evm/1 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'

# Get the latest block (header only, no transactions)
curl http://obol.stack/rpc/rpc/mainnet \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["latest",false],"id":1}'

# Call a contract (eth_call)
curl http://obol.stack/rpc/rpc/evm/1 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_call","params":[{"to":"0x<address>","data":"0x<calldata>"},"latest"],"id":1}'

# Get transaction receipt
curl http://obol.stack/rpc/rpc/evm/1 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_getTransactionReceipt","params":["0x<txhash>"],"id":1}'

# Get logs — eRPC auto-splits large block ranges to respect upstream rate limits
curl http://obol.stack/rpc/rpc/evm/1 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_getLogs","params":[{"address":"0x<addr>","fromBlock":"0x<from>","toBlock":"0x<to>"}],"id":1}'

# Hoodi testnet
curl http://obol.stack/rpc/rpc/evm/560048 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Per-Request Directives

Override eRPC behaviour for individual requests via HTTP headers or query parameters:

| Directive | Header | Query param | Effect |
|-----------|--------|-------------|--------|
| Skip cache | `X-ERPC-Skip-Cache-Read: true` | `?skip-cache-read=true` | Bypass in-memory cache, force upstream call |
| Pin upstream | `X-ERPC-Use-Upstream: erpc-gcp` | `?use-upstream=erpc-gcp` | Route to a specific upstream by ID |
| No empty retry | `X-ERPC-Retry-Empty: false` | `?retry-empty=false` | Don't retry when upstream returns empty/null |
| No pending retry | `X-ERPC-Retry-Pending: false` | `?retry-pending=false` | Don't retry unconfirmed transactions |

Example — force fresh data bypassing the 10-second in-memory cache:
```bash
curl http://obol.stack/rpc/rpc/evm/1 \
  -H 'Content-Type: application/json' \
  -H 'X-ERPC-Skip-Cache-Read: true' \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":1}'
```

## Health Check

eRPC's `/healthcheck` endpoint lives on port 4000 directly — it is not exposed through the
Traefik `/rpc` route. Use `obol kubectl port-forward` to reach it from the host:

```bash
# Forward erpc port 4000 to localhost (run in background or separate terminal)
obol kubectl port-forward -n erpc svc/erpc 4000:4000 &

# Check health — returns 200 OK when healthy
curl http://localhost:4000/healthcheck
```

## Metrics (Prometheus)

eRPC exposes Prometheus metrics on a separate port (4001):

```bash
# Forward metrics port
obol kubectl port-forward -n erpc svc/erpc 4001:4001 &

# Scrape metrics
curl http://localhost:4001/metrics
```

Key metrics to watch:
- `erpc_upstream_request_total` — total requests per upstream
- `erpc_upstream_request_duration_seconds` — latency histograms (P90, P99)
- `erpc_network_request_total` — requests per network
- `erpc_cache_hits_total` / `erpc_cache_misses_total` — cache effectiveness

## Checking eRPC Status

```bash
# Is the pod running?
obol kubectl get pods -n erpc

# Tail logs
obol kubectl logs -n erpc deployment/erpc --tail=100 -f

# Describe deployment (events, resource limits, readiness)
obol kubectl describe deployment -n erpc erpc
```

## Routing Architecture

The full request path from host to upstream:

```
Host / Browser
    │  http://obol.stack/rpc/rpc/evm/1
    ▼
Traefik  (traefik ns, port 80)
    │  PathPrefix /rpc → erpc svc:4000   [no prefix stripping]
    ▼
eRPC  (erpc ns, port 4000)
    │  project=rpc, arch=evm, chainId=1
    │  in-memory cache (10 000 items, TTL=10s for unfinalized)
    │  failsafe: timeout 30s, retry 2×, hedge 500ms
    ▼
Obol hosted gateway  (erpc.gcp.obol.tech)
    │  Basic-auth credential embedded in chart; rate-limited
    ▼
Ethereum mainnet / Hoodi testnet
```

## Key Configuration Files

| File | Purpose |
|------|---------|
| `internal/embed/infrastructure/values/erpc.yaml.gotmpl` | Helm values: image, project config, networks, failsafe, CORS |
| `internal/embed/infrastructure/helmfile.yaml` (lines 124–161) | Deploys eRPC chart + HTTPRoute via Traefik |

The upstream URL template:
```
https://<user>:<pass>@erpc.gcp.obol.tech/<network>/evm/<chainId>
```
Credentials are embedded and intentionally included in source (rate-limited convenience proxy).
See the `gitleaks:allow` comment in `erpc.yaml.gotmpl`.
