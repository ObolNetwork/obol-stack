# ADR-0001: Local-First Kubernetes via k3d

**Status:** Accepted
**Date:** 2026-03-27

## Context

Obol Stack needs a reproducible local Kubernetes cluster that supports:

- Port forwarding from the host (80, 443, 8080, 8443) for Traefik ingress.
- Docker image import for locally built images (x402-verifier, x402-buyer) during development.
- Fast startup times (under 60 seconds) for developer iteration.
- Consistent behavior across macOS and Linux.
- Access to host services (Ollama) from within the cluster.

The main alternatives considered were:

| Option | Pros | Cons |
|--------|------|------|
| **k3d** | Docker-based, fast startup, native image import, multi-platform, port mapping via k3d config | Requires Docker, k3s-only |
| **minikube** | Multi-driver (Docker, HyperKit, VirtualBox), wide adoption | Slower startup, heavier resource usage, image import via registry or `minikube image load` |
| **kind** | Docker-based, widely used for CI | No native port mapping (requires manual extraPortMappings), no built-in Ollama host routing |
| **bare-metal k3s** | No Docker dependency, direct host access | Requires root or systemd, harder to isolate, no image import |

## Decision

Use **k3d** as the default Kubernetes backend for local development and operation.

Additionally, implement a `Backend` interface (`internal/stack/backend.go`) to abstract the runtime, allowing a secondary `K3sBackend` for bare-metal deployments where Docker is unavailable (e.g., production edge nodes).

## Rationale

1. **Port forwarding**: k3d natively maps host ports to the k3s server in its YAML config, avoiding manual iptables or NodePort workarounds.
2. **Image import**: `k3d image import` loads locally built Docker images directly into the cluster, critical for `OBOL_DEVELOPMENT=true` builds of x402-verifier and x402-buyer.
3. **Fast startup**: k3d cluster creation completes in 10-30 seconds, compared to 60-120 seconds for minikube.
4. **Host access**: k3d provides `host.docker.internal` (macOS) and `host.k3d.internal` (Linux) for Ollama connectivity.
5. **k3s compatibility**: k3d wraps k3s, so manifests placed in `/var/lib/rancher/k3s/server/manifests/` auto-apply on startup -- used for infrastructure deployment.

## Consequences

### Positive

- Reproducible single-cluster setup with a declarative k3d YAML config.
- `obol stack up` reliably creates, configures, and tears down clusters.
- Development workflow is fast: build image, import, restart pod.
- The `Backend` interface means k3s bare-metal is also supported without code duplication.

### Negative

- **Docker dependency**: Operators must have Docker or Podman running. This excludes minimal environments without containerization.
- **Single cluster**: One k3d cluster per config directory. Multiple stacks require separate `OBOL_CONFIG_DIR` values.
- **Port conflicts**: k3d binds host ports 80/443/8080/8443 directly; other services using these ports cause startup failure.
- **Kubeconfig port drift**: The k3d API server port can change between cluster restarts, requiring `k3d kubeconfig write` to refresh.
- **ConfigMap propagation delay**: k3d's file watcher introduces 60-120 second delays for manifest changes placed in the k3s manifests directory.
- **Ollama host resolution varies**: `host.docker.internal` on macOS, `host.k3d.internal` on Linux, `127.0.0.1` for k3s -- resolved at `obol stack init` time.

## SPEC References

- Section 2.4 -- Backend Abstraction
- Section 3.1 -- Stack Lifecycle
- Section 1.3 -- System Constraints (absolute paths, single cluster)
- Section 3.1.4 -- Ollama Host Resolution
