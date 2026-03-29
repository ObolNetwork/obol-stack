# ADR-0005: Traefik with Kubernetes Gateway API

**Status:** Accepted
**Date:** 2026-03-27

## Context

Obol Stack requires an ingress layer that supports:

- **Per-route middleware**: x402 ForwardAuth must apply only to `/services/*` routes, not to all traffic.
- **Hostname-based access control**: Internal services (frontend, eRPC, monitoring) must be restricted to `obol.stack` hostname, while public routes (x402-gated services, discovery endpoints) must be accessible via the Cloudflare tunnel hostname.
- **Dynamic route creation**: The monetize reconciler creates HTTPRoutes programmatically when ServiceOffers reach the RoutePublished stage.
- **Standard CRDs**: Routes should be managed as Kubernetes resources with ownerReferences for automatic garbage collection.

Alternatives considered:

| Option | Pros | Cons |
|--------|------|------|
| **Traefik + Gateway API** | Per-route middleware via Middleware CRD, hostname filtering on HTTPRoute, standard K8s Gateway API CRDs, built into k3s | Traefik-specific Middleware CRD (`traefik.io`), newer API surface |
| **Traefik + Ingress** | Simple, widely supported | No per-route middleware (annotations are per-Ingress), hostname restrictions are less granular |
| **Nginx Ingress** | Mature, widely deployed | No native ForwardAuth per route (requires custom annotations), no Gateway API support in standard controller |
| **Istio service mesh** | Full mTLS, advanced routing | Heavy resource footprint, complex for a local-first stack, overkill for HTTP routing |
| **Envoy Gateway** | Gateway API native | Less mature, no built-in ForwardAuth equivalent, additional deployment |

## Decision

Use **Traefik** as the cluster ingress controller with the **Kubernetes Gateway API** (GatewayClass, Gateway, HTTPRoute) for routing, combined with Traefik-specific **Middleware** CRDs (`traefik.io`) for ForwardAuth.

## Rationale

1. **Built into k3s**: Traefik is the default ingress controller in k3s/k3d. No additional installation or configuration needed.
2. **Gateway API HTTPRoute**: The `HTTPRoute` CRD supports `hostnames` filtering natively. Setting `hostnames: ["obol.stack"]` on internal routes ensures they are never matched by tunnel traffic (which arrives with the tunnel hostname).
3. **ForwardAuth Middleware**: Traefik's `Middleware` CRD (`traefik.io/v1alpha1`) supports `forwardAuth` configuration. The x402-verifier is referenced as a ForwardAuth target on per-route HTTPRoutes, so only `/services/*` traffic is payment-gated.
4. **OwnerReferences**: HTTPRoutes and Middlewares created by the monetize reconciler set ownerReferences to the ServiceOffer CR. Deleting a ServiceOffer cascades deletion to all routing resources.
5. **Single Gateway**: One `Gateway` resource (`traefik-gateway` in `traefik` namespace) handles all HTTP/HTTPS traffic. Routes reference it via `parentRefs`.
6. **Security by default**: The hostname restriction pattern makes it structurally impossible to accidentally expose internal services via the tunnel. Adding a new internal service requires explicitly setting `hostnames: ["obol.stack"]`.

## Consequences

### Positive

- Clean separation between local-only routes (hostname-restricted) and public routes (no hostname restriction).
- The reconciler creates standard Kubernetes resources (HTTPRoute, Middleware) that are visible via `kubectl` and benefit from RBAC.
- ForwardAuth is applied per-route, not globally. Free routes (health, discovery) bypass the verifier entirely.
- Automatic garbage collection via ownerReferences prevents orphaned routes when ServiceOffers are deleted.
- The routing architecture is auditable: `kubectl get httproutes -A` shows all routes with their hostname restrictions.

### Negative

- **Traefik-specific Middleware**: The `Middleware` CRD is not part of the standard Gateway API. This couples the stack to Traefik. Migrating to another Gateway API controller would require replacing ForwardAuth with a different mechanism.
- **ExternalName incompatibility**: Traefik's Gateway API implementation does not support `ExternalName` Services. All upstreams must use `ClusterIP` + `Endpoints`, which required workarounds for cross-namespace routing.
- **GatewayClass singleton**: Only one `GatewayClass` (`traefik`) exists. Multi-tenant scenarios with different ingress controllers are not supported.
- **No mTLS**: Traefik in this configuration does not provide mutual TLS between services. Inter-service communication within the cluster is unencrypted (acceptable for a local-first stack).
- **Hostname discipline required**: Developers must remember to add `hostnames: ["obol.stack"]` to every internal HTTPRoute. The SPEC and CLAUDE.md document this as a security invariant, and code review must enforce it.

## SPEC References

- Section 2.2 -- Routing Architecture
- Section 3.4.4 -- x402-verifier (ForwardAuth)
- Section 7.1 -- Tunnel Exposure (security model, hostname restrictions)
- Section 5.2 -- Kubernetes Resources (traefik namespace)
- Section 7.5 -- RBAC (Middleware CRD access)
