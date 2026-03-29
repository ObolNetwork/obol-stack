# Extract reth-erc8004-indexer into standalone Helm chart with discovery fallback

## Summary

Carve the `reth-erc8004-indexer/` component out of PR #288 into its own PR with a dedicated Helm chart, proper test coverage, and a multi-tier discovery architecture that can fall back to Etherscan/BaseScan's native ERC-8004 metadata support when a full Reth node is impractical.

## Motivation

The reth-erc8004-indexer currently lives as a loose directory in the repo with a Dockerfile but no Helm chart, no CI, and no tests beyond ralph-m3's manual validation checklist. The autoresearch coordinator (and any future service that needs agent discovery) depends on a working ERC-8004 query API, but today that dependency is either:

1. **8004scan.io** — a third-party centralized API we don't control
2. **reth-erc8004-indexer** — a custom Reth binary that requires syncing an entire Base L2 node

Neither option is great for all deployment scenarios. Meanwhile, **Etherscan/BaseScan announced native ERC-8004 metadata display** (operational status, x402 support, services) on NFT detail pages. This creates a third discovery tier that's reliable, free, and doesn't require running infrastructure.

The indexer should ship as a properly tested, independently installable Helm chart that the autoresearch chart (and others) can declare as an optional dependency.

## Scope

### In scope

- [ ] Move `reth-erc8004-indexer/` and `Dockerfile.reth-erc8004-indexer` into a self-contained Helm chart at `charts/reth-erc8004-indexer/`
- [ ] Implement 3-tier discovery fallback in the coordinator's discovery client
- [ ] Integration tests for the indexer API surface
- [ ] CI pipeline for building the Reth binary image
- [ ] Document deployment scenarios (full node, lightweight, external-only)

### Out of scope

- Autoresearch reward engine or OPOW mechanics (separate issue)
- Changes to the autoresearch coordinator loop logic
- ERC-8004 registration/minting changes

## Architecture: 3-Tier Discovery

The coordinator and any other discovery consumer should attempt sources in priority order:

```
Priority 1: Internal Reth Indexer (self-hosted, real-time)
    │
    │  OBOL_INDEXER_API_URL=http://reth-indexer:3400
    │  Latency: <100ms, block-level freshness
    │  Cost: runs a full Base L2 node (~500GB disk, ongoing sync)
    │
    ▼ if unavailable or not deployed
Priority 2: BaseScan / Etherscan API (hosted, reliable)
    │
    │  BASESCAN_API_URL=https://api.basescan.org/api
    │  BASESCAN_API_KEY=<user's key>
    │  Latency: <500ms, near real-time
    │  Cost: free tier = 5 calls/sec, Pro = 100K calls/day
    │  Coverage: ERC-8004 metadata now displayed natively
    │    - agent operational status
    │    - x402 support flag
    │    - registered services list
    │    - NFT detail page with full metadata
    │
    ▼ if unavailable or no API key
Priority 3: 8004scan.io (community, best-effort)
    │
    │  SCAN_API_URL=https://www.8004scan.io/api/v1/public
    │  Latency: <1s, minutes behind chain
    │  Cost: free, no key required
    │  Risk: third-party, no SLA
    │
    ▼ if all unavailable
    Error: no discovery backend available
```

### BaseScan Integration Details

As of March 26, 2026, BaseScan displays ERC-8004 metadata on NFT detail pages:
- Contract: `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432` (18,512 holders, 45,198 transfers)
- Each agent NFT page shows: operational status, x402 support, services, metadata
- BaseScan API can query token holders, transfer events, and read contract state

The BaseScan adapter needs to:
1. Query NFT holders of the Identity Registry contract
2. For each token ID, read the metadata URI via `tokenURI(tokenId)`
3. Fetch the off-chain registration JSON from the metadata URI
4. Filter by OASF skill/domain taxonomy (same as 8004scan queries)
5. Cache results with configurable TTL (default: 5 minutes)

This is more work per query than the indexer (N+1 calls vs single query), but it requires **zero infrastructure** and uses a highly reliable API.

### Discovery Client Interface

```go
// internal/discovery/discovery.go

type Agent struct {
    TokenID     string
    ChainID     uint64
    Owner       string
    Name        string
    Endpoint    string
    Skills      []string
    Domains     []string
    Metadata    map[string]interface{}
    X402Support bool
}

type DiscoveryClient interface {
    ListAgents(ctx context.Context, opts ListOptions) ([]Agent, error)
    SearchAgents(ctx context.Context, query string, limit int) ([]Agent, error)
    GetAgent(ctx context.Context, chainID uint64, tokenID string) (*Agent, error)
    Health(ctx context.Context) error
}

type ListOptions struct {
    Skill      string   // OASF skill filter, e.g. "devops_mlops/model_versioning"
    Domain     string   // OASF domain filter
    ChainID    uint64   // filter by chain
    Limit      int
    SortBy     string   // "registered_at", "name"
}
```

Three implementations: `RethIndexerClient`, `BaseScanClient`, `EightKScanClient`.
A `FallbackClient` wraps all three and tries in priority order.

### Cluster Topology

```
┌─────────────────────────────────────────────────────────────┐
│  obol-stack cluster (k3d / k3s)                             │
│                                                             │
│  ┌─────────────────────────┐   ┌──────────────────────┐    │
│  │ reth-erc8004-indexer    │   │ autoresearch chart    │    │
│  │ (optional Helm chart)   │   │ (depends on discovery)│    │
│  │                         │   │                       │    │
│  │ ┌─────────┐ ┌────────┐ │   │  coordinator          │    │
│  │ │ Reth    │ │ SQLite │ │   │    │                   │    │
│  │ │ ExEx    │→│  WAL   │ │   │    ▼                   │    │
│  │ │ (Base)  │ │ store  │ │   │  FallbackClient        │    │
│  │ └─────────┘ └────────┘ │   │    ├→ RethIndexer?    │    │
│  │       │                │   │    ├→ BaseScan?        │    │
│  │  ┌────▼──────┐         │   │    └→ 8004scan?       │    │
│  │  │ REST API  │◄────────│───│─── GET /agents?skill= │    │
│  │  │ :3400     │         │   │                       │    │
│  │  └───────────┘         │   └──────────────────────┘    │
│  └─────────────────────────┘                                │
│                                                             │
│           OR (lightweight mode)                             │
│                                                             │
│  ┌──────────────────────┐                                   │
│  │ autoresearch chart   │                                   │
│  │                      │     ┌──────────────────────┐      │
│  │  FallbackClient ─────│────→│ api.basescan.org     │      │
│  │  (no indexer needed) │     │ (ERC-8004 metadata   │      │
│  │                      │     │  natively supported) │      │
│  └──────────────────────┘     └──────────────────────┘      │
└─────────────────────────────────────────────────────────────┘
```

## Helm Chart Structure

```
charts/reth-erc8004-indexer/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── _helpers.tpl
│   ├── statefulset.yaml          # Reth + ExEx + API in one pod
│   ├── service.yaml              # ClusterIP on port 3400 (API) + 30303 (P2P)
│   ├── pvc.yaml                  # Persistent volume for chain data + SQLite
│   ├── configmap.yaml            # Reth config (Base chain, ExEx params)
│   ├── servicemonitor.yaml       # Prometheus metrics (optional)
│   └── tests/
│       └── test-api.yaml         # Helm test: curl /health + /api/v1/public/stats
└── README.md
```

### values.yaml (key fields)

```yaml
replicaCount: 1

image:
  repository: ghcr.io/obolnetwork/reth-erc8004-indexer
  tag: latest

reth:
  chain: base
  dataDir: /data/reth
  syncMode: full           # full | archive
  httpPort: 8545
  p2pPort: 30303

indexer:
  apiPort: 3400
  dbPath: /data/indexer.db
  identityRegistry: "0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
  reputationRegistry: "0x8004BAa17C55a88189AE136b182e5fdA19dE9b63"

persistence:
  enabled: true
  size: 500Gi              # Base L2 chain data
  storageClass: ""          # use cluster default

resources:
  requests:
    cpu: 2
    memory: 8Gi
  limits:
    cpu: 4
    memory: 16Gi
```

## Test Plan

### Unit tests (Rust)

- [ ] `storage.rs`: insert/query/update/delete agents in SQLite
- [ ] `storage.rs`: pagination, sorting, search with LIKE/FTS
- [ ] `indexer.rs`: parse `Registered`, `URIUpdated`, `MetadataSet` event logs
- [ ] `indexer.rs`: handle reorgs (rollback indexed data on chain reorg)
- [ ] `api.rs`: response shape matches 8004scan API contract

### Integration tests (against running instance)

- [ ] `/health` returns 200 with sync status
- [ ] `/api/v1/public/agents` returns paginated list
- [ ] `/api/v1/public/agents?protocol=OASF&search=model_versioning` filters correctly
- [ ] `/api/v1/public/agents/{chain_id}/{token_id}` returns single agent with full metadata
- [ ] `/api/v1/public/stats` returns registry statistics
- [ ] Response shapes are wire-compatible with 8004scan (coordinator works against both)

### Discovery fallback tests

- [ ] FallbackClient uses Reth indexer when available
- [ ] FallbackClient falls back to BaseScan when indexer is down
- [ ] FallbackClient falls back to 8004scan when BaseScan has no API key
- [ ] FallbackClient returns error when all three are unavailable
- [ ] BaseScan adapter correctly reads ERC-8004 NFT metadata via token API
- [ ] Cache TTL is respected (no redundant API calls within window)

### Autoresearch-specific tests

- [ ] Coordinator discovers workers with `devops_mlops/model_versioning` skill via each tier
- [ ] Coordinator reads `best_val_bpb` from worker metadata via each tier
- [ ] Coordinator probes discovered workers via x402 (402 response = alive)
- [ ] End-to-end: register worker → indexer picks up → coordinator discovers → probe succeeds

## Migration from PR #288

Files to move into this PR:

```
reth-erc8004-indexer/          → charts/reth-erc8004-indexer/src/  (or keep at root with chart alongside)
Dockerfile.reth-erc8004-indexer → charts/reth-erc8004-indexer/Dockerfile
ralph-m3.md                    → reference for test plan, then remove
```

New files:

```
charts/reth-erc8004-indexer/    → Helm chart (as above)
internal/discovery/             → Go discovery client with fallback
internal/discovery/discovery.go → interface + FallbackClient
internal/discovery/reth.go      → RethIndexerClient
internal/discovery/basescan.go  → BaseScanClient
internal/discovery/eightkcan.go → EightKScanClient (8004scan)
internal/discovery/*_test.go    → tests for each
```

## Acceptance Criteria

1. `helm install indexer charts/reth-erc8004-indexer` deploys and syncs on a k3s cluster with Base chain
2. Coordinator discovers workers via the indexer with zero code changes to coordinate.py (SCAN_API_URL points to indexer)
3. When indexer is not installed, coordinator automatically falls back to BaseScan or 8004scan
4. All tests in the test plan pass in CI
5. Docker image builds in CI and publishes to ghcr.io/obolnetwork/reth-erc8004-indexer

## Labels

`component:indexer` `component:discovery` `priority:high` `size:L`
