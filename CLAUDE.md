# Obol Stack

## Purpose

Bootstrap script for local Kubernetes development using k3d (k3s in Docker). Deploys a monitoring stack with Prometheus and Grafana via declarative helmfile manifests.

## Technologies

- **k3d (v5.7.5)**: Lightweight Kubernetes distribution (k3s) running in Docker containers
- **k3s**: Configuration reference at https://docs.k3s.io/installation/configuration
- **helm (v3.19.0)**: Kubernetes package manager for deploying charts
- **helmfile (v1.1.7)**: Declarative spec for deploying helm charts
- **k9s (v0.50.15)**: Terminal UI for Kubernetes cluster management
- **Prometheus**: Metrics collection and storage with cAdvisor, node-exporter, and kube-state-metrics
- **Grafana**: Metrics visualization with Kubernetes cluster monitoring dashboards

## Helmfile Approach

All cluster resources are declaratively defined in `manifests/helmfile.yaml` as a **single source of truth**:

1. **Grafana release**: Deploys Grafana with anonymous auth enabled, ingress at `grafana.localhost`
2. **Prometheus release**: Deploys Prometheus with node exporters and kube-state-metrics
3. **Dashboard chart**: Local helm chart deploying Grafana dashboard ConfigMaps

The script syncs manifests to `~/.config/obol/manifests/` and applies them via helmfile during cluster bootstrap.

**Key principle**: All cluster changes should be made by editing `manifests/helmfile.yaml` (or charts within `manifests/`) and applying via a single `helmfile apply` command. This ensures the cluster state is fully reproducible and version-controlled.

**CRITICAL**: `obolup.sh` must NOT perform any out-of-band cluster mutations (no direct `kubectl`, `helm uninstall`, etc.). All cluster state changes happen exclusively through `helmfile apply`. The script only:
1. Downloads binaries
2. Creates k3d cluster (if missing)
3. Syncs manifest files
4. Runs `helmfile apply`

Any cleanup or migration logic belongs in helmfile or pre-sync hooks, never in the bootstrap script.

**Best practices**: https://helmfile.readthedocs.io

## Installation

```bash
./obolup.sh
```

Downloads tools to `~/.config/obol/bin/`, creates k3d cluster from `k3d-config.yaml`, applies helmfile manifests.

## k3d Configuration

Cluster defined in `k3d-config.yaml`. Node labels set via k3s `--node-label` arg (see https://docs.k3s.io/installation/configuration).

Config synced to `~/.config/obol/k3d-config.yaml` during bootstrap.

## Cluster Access

**Kubeconfig**: `~/.config/obol/kubeconfig.yaml`

```bash
# Set kubeconfig
export KUBECONFIG=~/.config/obol/kubeconfig.yaml

# Using kubectl directly
kubectl get pods -n monitoring

# Using obolup.sh proxies
./obolup.sh kubectl get pods -n monitoring
./obolup.sh helm list -n monitoring
./obolup.sh helmfile list
./obolup.sh k9s
```

**Grafana UI**: http://grafana.localhost (anonymous access enabled)

## Manifest Structure

Manifests are organized in a readable, categorized structure:

```
manifests/
├── helmfile.yaml              # Main helmfile - single source of truth
├── dashboards-chart/          # Grafana dashboards (local helm chart)
│   ├── Chart.yaml
│   └── templates/
│       └── configmap.yaml     # Dashboard definitions
└── [future additions organized by category/purpose]
```

**Organization principles**:
- Group related resources into subdirectories (e.g., `dashboards-chart/`, `monitoring/`)
- Use descriptive directory names that indicate purpose
- Keep `helmfile.yaml` clean by referencing local charts for complex configurations
- Each chart should be self-contained with its own `Chart.yaml` and `templates/`

## Manifest Management

Edit manifests in `manifests/` directory, then redeploy:

```bash
# Manifests are synced from repo to ~/.config/obol/manifests/ during obolup.sh execution
./obolup.sh  # Syncs manifests and applies helmfile

# Or manually sync and apply
cp -r manifests/* ~/.config/obol/manifests/
cd ~/.config/obol/manifests
KUBECONFIG=~/.config/obol/kubeconfig.yaml helmfile apply
```

## Development Workflow

1. **Modify manifests** in `manifests/helmfile.yaml` or `manifests/dashboards-chart/`
2. **Test changes**: Run `./obolup.sh` to sync manifests and apply via helmfile
3. **Verify deployment**: Use `./obolup.sh k9s` or `./obolup.sh kubectl get pods -n monitoring`

Script automatically syncs `manifests/` → `~/.config/obol/manifests/` on each run (see `obolup.sh:260-268`).
