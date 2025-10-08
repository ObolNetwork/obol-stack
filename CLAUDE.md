# Obol Stack

## Purpose

Bootstrap script for local Kubernetes development using k3d (k3s in Docker). Deploys a monitoring stack with Prometheus and Grafana using ArgoCD GitOps with helmfile and OCI registry artifacts.

## Technologies

- **k3d (v5.7.5)**: Lightweight Kubernetes distribution (k3s) running in Docker containers with embedded OCI registry
- **k3s**: Configuration reference at https://docs.k3s.io/installation/configuration
- **helm (v3.19.0)**: Kubernetes package manager for deploying charts
- **helmfile (v1.1.7)**: Declarative spec for deploying helm charts (used by ArgoCD plugin)
- **argocd (v3.1.8)**: GitOps continuous delivery with helmfile plugin support
- **k9s (v0.50.15)**: Terminal UI for Kubernetes cluster management
- **Prometheus**: Metrics collection and storage with cAdvisor, node-exporter, and kube-state-metrics
- **Grafana**: Metrics visualization with Kubernetes cluster monitoring dashboards

## ArgoCD GitOps Approach

All cluster resources are declaratively defined in `manifests/helmfile.yaml` and deployed via **ArgoCD with native helmfile support**:

**Architecture:**
```
manifests/helmfile.yaml (source of truth)
   ↓ (obolup.sh syncs)
~/.config/obol/manifests/
   ↓ (package as OCI artifact)
OCI Registry (localhost:5000/obol-manifests:latest)
   ↓ (ArgoCD watches)
argo-cd-helmfile plugin (templates helmfile)
   ↓ (ArgoCD applies + prunes)
Cluster resources (1:1 with helmfile)
```

**Key components:**
1. **OCI Registry**: k3d-embedded registry at `localhost:5000`
2. **ArgoCD**: GitOps controller with web UI at `localhost:8080`
3. **argo-cd-helmfile plugin**: Native helmfile.yaml support in ArgoCD (https://github.com/travisghansen/argo-cd-helmfile)
4. **ArgoCD Application**: Configured with `prune: true` and `selfHeal: true`

**Deployment flow:**
1. Edit `manifests/helmfile.yaml` (or charts within `manifests/`)
2. Run `./obolup.sh` → syncs to `~/.config/obol/manifests/` and pushes to OCI
3. ArgoCD detects new OCI artifact (3-minute polling interval)
4. Plugin runs `helmfile template` to generate manifests
5. ArgoCD applies changes and prunes orphaned resources

**Key principle**: 
- **Source of truth**: `manifests/helmfile.yaml` (native helmfile syntax)
- **Automatic pruning**: ArgoCD removes resources not in helmfile (via `prune: true`)
- **Drift correction**: ArgoCD reverts manual changes (via `selfHeal: true`)
- **No manual kubectl**: All changes via manifests → ArgoCD reconciliation

**obolup.sh responsibilities**:
1. Download binaries (k3d, helm, helmfile, argocd, k9s)
2. Create k3d cluster with OCI registry
3. Bootstrap ArgoCD with helmfile plugin
4. Create ArgoCD Application
5. Sync manifests and push to OCI registry

**Benefits over pure helmfile**:
- ✓ Automatic orphaned resource pruning
- ✓ Continuous drift detection and correction
- ✓ Web UI for visualization
- ✓ Native helmfile.yaml syntax (no conversion)
- ✓ True 1:1 mapping between helmfile and cluster

**References**:
- ArgoCD docs: https://argo-cd.readthedocs.io/
- argo-cd-helmfile: https://github.com/travisghansen/argo-cd-helmfile
- Helmfile: https://helmfile.readthedocs.io

## Installation

```bash
./obolup.sh
```

Downloads tools to `~/.config/obol/bin/`, creates k3d cluster from `k3d-config.yaml`, bootstraps ArgoCD with helmfile plugin, pushes manifests to OCI registry.

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
./obolup.sh argocd app get obol-stack
./obolup.sh k9s
```

**Grafana UI**: http://grafana.localhost (anonymous access enabled)
**ArgoCD UI**: http://localhost:8080 (get password: `kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d`)

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
# Automatic sync and deploy
./obolup.sh  # Syncs manifests, pushes to OCI, ArgoCD syncs within 3 minutes

# Monitor ArgoCD sync status
./obolup.sh argocd app get obol-stack
./obolup.sh argocd app sync obol-stack  # Force immediate sync

# View ArgoCD logs
./obolup.sh kubectl logs -n argocd -l app.kubernetes.io/name=argocd-server -f

# ArgoCD Web UI (recommended)
open http://localhost:8080
```

## Development Workflow

1. **Modify manifests** in `manifests/helmfile.yaml` or dashboard templates
2. **Deploy changes**: Run `./obolup.sh` to push to OCI registry
3. **Wait for ArgoCD**: ArgoCD syncs within 3 minutes (or force with `argocd app sync`)
4. **Verify deployment**: 
   - ArgoCD UI: http://localhost:8080
   - CLI: `./obolup.sh argocd app get obol-stack`
   - k9s: `./obolup.sh k9s`

**Orphaned resource cleanup**: ArgoCD automatically prunes resources removed from helmfile (via `prune: true`).

**Drift correction**: Manual changes via `kubectl` are automatically reverted by ArgoCD (via `selfHeal: true`).

**Debugging**:
- View ArgoCD Application status: `./obolup.sh argocd app get obol-stack`
- Check sync errors: ArgoCD UI → Applications → obol-stack → Details
- View helmfile plugin logs: `kubectl logs -n argocd -l app.kubernetes.io/name=argocd-repo-server -c helmfile-plugin`
