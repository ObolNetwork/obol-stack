# Applications Directory

This directory contains application templates and configurations for obol-stack.

## Quick Summary

- **`default/` applications**: Automatically installed during `obol cluster init` and deployed on cluster startup
- **Other applications**: Must be installed via `obol app install <app-name>` (not yet implemented)

## Structure

```
applications/
├── default/              # Embedded applications (auto-deployed with every cluster)
│   └── monitoring/       # Prometheus + Grafana monitoring stack
└── <app-name>/           # Installable applications (Helmfiles with sane defaults)
    ├── helmfile.yaml     # Application definition
    ├── values.yaml       # Sane defaults
    └── README.md         # Configuration guide
```

## Application Types

### Default Applications (`default/`)

**Purpose:** Foundational platform services embedded in every obol-stack installation.

**Lifecycle:**
- Embedded in binary and extracted to `~/.config/obol/applications/default/` during `obol cluster init`
- Mounted into k3s at `/var/lib/rancher/k3s/server/manifests/default/`
- Applied automatically on cluster startup via k3s manifest auto-apply mechanism
- Always present - no installation command needed

**Current applications:**
- **monitoring**: kube-prometheus-stack (Prometheus, Grafana, Alertmanager, exporters)
  - Grafana: http://grafana.localhost:8080 (anonymous access)
  - Prometheus: http://prometheus.localhost:8080
  - Pre-configured with Kubernetes dashboards
  - Persistent storage for metrics and dashboards

**When to use:**
- Core infrastructure needed by all clusters
- Services that should always be available
- Platform capabilities that other applications depend on

### Installable Applications

**Purpose:** Optional applications installed via CLI with user-configurable options.

**Note:** `obol app install` and `obol app apply` are not yet implemented.

**Installation Flow (planned):**
```bash
# Install application from repo (downloads to local cluster config)
obol app install <app-name>

# Prompts for configuration or uses sane defaults
# Creates: ~/.config/obol/applications/<app-name>/

# Edit configuration to user preferences
vim ~/.config/obol/applications/<app-name>/values.yaml

# Apply application (uses helmfile + applyset for tracking)
obol app apply <app-name>

# Kubernetes tracks all resources via applyset label
# Changing configuration and re-applying prunes/updates resources automatically
```

**Structure:** Each application contains Helmfiles with:
- **Sane defaults** - Works out of the box
- **User-editable values** - Customize to preferences
- **Applyset tracking** - All resources labeled for lifecycle management

**Example Applications:**

**`ethereum`** - Ethereum consensus + execution client stack
- Uses ethpandaops Helm charts
- Values file selects client combination:
  ```yaml
  consensus:
    client: lighthouse  # or prysm, lodestar, teku, nimbus
  execution:
    client: reth        # or geth, nethermind, besu, erigon
  ```
- Switch clients by editing values and re-applying
- Applyset ensures old client resources are pruned when switching

**`charon`** - Distributed Validator Technology client
- Obol Network DVT client
- Configurable for single node or cluster setup

**`charon-cluster`** - Multi-node Charon cluster
- Deploys complete DVT cluster
- Configurable node count and validator assignments

**Applyset Lifecycle:**

When you run `obol app apply <app-name>`, resources are labeled:
```yaml
metadata:
  labels:
    applyset.kubernetes.io/id: <app-name>
```

This enables:
- **Automatic pruning** - Resources removed from config are deleted
- **Resource tracking** - Kubernetes knows what belongs to each app
- **Safe updates** - Changing client combinations cleanly replaces resources

**Example: Switching Ethereum Clients**
```bash
# Install ethereum app
obol app install ethereum

# Configure for Lighthouse + Reth
vim ~/.config/obol/applications/ethereum/values.yaml
# Set: consensus.client=lighthouse, execution.client=reth

# Apply
obol app apply ethereum

# Later: switch to Prysm + Geth
vim ~/.config/obol/applications/ethereum/values.yaml
# Set: consensus.client=prysm, execution.client=geth

# Re-apply (old Lighthouse/Reth resources automatically pruned)
obol app apply ethereum
```

## Adding Installable Applications

1. Create application directory: `applications/<app-name>/`
2. Add `helmfile.yaml` with application definition
3. Add `values.yaml` with sane defaults
4. Add `README.md` documenting configuration options
5. Test installation: `obol app install <app-name>`

**Example Structure:**
```
applications/ethereum/
├── helmfile.yaml        # References ethpandaops charts
├── values.yaml          # Sane defaults + client selection
│                        # consensus.client: lighthouse
│                        # execution.client: reth
│                        # resource limits, network settings, etc.
└── README.md            # Configuration guide and client options
```

## Design Principles

- **default/**: Minimal, embedded, universal platform services
- **Everything else**: CLI-installed, Helmfile-based, applyset-tracked
- **Sane defaults + user customization**: Works out of box, editable for power users
- **Applyset tracking**: Clean lifecycle management with automatic pruning
- **Composable**: Mix and match client combinations (e.g., Prysm + Geth vs Lighthouse + Reth)
