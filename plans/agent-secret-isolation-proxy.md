# Step C, expanded: secret-injecting egress proxy (+ virtual keys)

Status: design options, written 2026-06-11. Companion to
plans/agent-business-architecture.md §C. Answers "what if step C were the
iron-proxy thing instead of just virtual keys?".

## The two are complementary, not alternatives

The original §C was per-agent LiteLLM virtual keys: budget + model
allowlist + spend attribution. That bounds the *damage* if an agent's
credential leaks. It does not stop the leak: the agent still HOLDS the key
(injected into hermes-config), so a prompt-injected or RCE'd seller agent —
one the public pays to run arbitrary tasks — can read and exfiltrate it.

A secret-injecting egress proxy attacks the other half: the agent holds
NOTHING. It makes outbound calls to a local proxy with no usable
credential; the proxy holds the real upstream secrets and injects them on
egress to allowlisted destinations only. Full compromise of the agent
container cannot read a secret that was never in its memory or env.

For agents the world pays to run, defense-in-depth wants both:

- **proxy** → the credential is never in the agent's reach (prevents leak)
- **virtual key** → whatever the proxy injects is itself bounded and
  revocable (limits blast radius if the proxy or an upstream is abused)

Recommended sequence: virtual keys first (smaller, immediate cost-control
value, and they become the credential the proxy injects), then the proxy as
the containment layer.

## Threat model recap

A seller agent's Hermes container today holds, in-process:

- the LiteLLM key (currently the shared master key; §C makes it a per-agent
  virtual key) — read from hermes-config
- since the rc15 work, the remote-signer bearer token (REMOTE_SIGNER_TOKEN)
- any API keys for paid upstreams it buys from

Any of these is one prompt-injection away from exfiltration. The
NetworkPolicy (Step B) stops the agent reaching OTHER namespaces' secrets,
but not from leaking its OWN outbound (it can reach the internet by design —
skills fetch URLs — which is also the exfiltration channel).

## Architecture: proxy as a sidecar in the Hermes pod

The strongest shape is a per-agent-pod sidecar (mirrors how x402-buyer is a
sidecar in the litellm pod), not a shared service:

```
Hermes container                  egress-proxy sidecar (same pod)
  REMOTE_SIGNER_URL ───────────►  localhost:<pp> ──┐ inject Bearer ──► remote-signer:9000
  LiteLLM base_url  ───────────►  localhost:<pp> ──┤ inject vkey   ──► litellm.llm:4000
  (no credentials in this        (holds the real    └ allowlist ─────► approved external APIs
   container's env at all)        secrets; L7 allow)
```

- Hermes' hermes-config points `base_url`/`REMOTE_SIGNER_URL` at
  `http://127.0.0.1:<proxyport>`; the credential fields are dropped from the
  Hermes container entirely.
- The proxy sidecar mounts the LiteLLM virtual key + signer token (from the
  per-namespace Secret) and injects the right header per destination.
- The proxy enforces an L7 destination allowlist — stricter than the
  L3/L4 NetworkPolicy: it can allow `litellm.llm:4000` and a named set of
  external hosts while denying everything else, and can log/meter calls.
- Bonus: the NetworkPolicy can then forbid the Hermes container's *own*
  egress to the credentialed services and require it to go through the
  sidecar (enforced by the proxy holding the only credential anyway).

Why sidecar over shared service: localhost binding means the proxy is never
network-reachable, so no cross-pod ACL is needed for it, and each agent's
secrets stay in its own pod. Cost is one more container per agent — cheap.

## Implementation options for the proxy

1. **iron-proxy (github.com/ironsh/iron-proxy)** — the tool the user
   flagged. PRE-WORK: actually evaluate it (license, maintenance, does it do
   per-destination credential injection + allowlist, config surface,
   image size, does it speak the HTTP shapes we need incl. SSE streaming
   for inference). If it fits, we ship it as the sidecar image and render
   its config from the Agent CR. RISK: unverified; it may be a generic
   reverse proxy rather than a secret-injector.
2. **Purpose-built Go sidecar** — we already build distroless Go sidecars
   (x402-buyer). A ~few-hundred-line proxy: read a destination→credential
   map + allowlist from a mounted config, inject headers, stream bodies
   through (the verifier's Flush discipline from CLAUDE.md applies — must
   forward http.Flusher for SSE). Full control, no external dependency,
   matches the codebase. Likely the pragmatic choice if iron-proxy doesn't
   cleanly fit.

Decide between them after a timeboxed iron-proxy spike.

## What the proxy injects, by destination

| Destination | Injected credential | Source |
|---|---|---|
| `litellm.llm:4000` | per-agent virtual key (Authorization) | §C virtual-key Secret |
| `remote-signer:9000` | REMOTE_SIGNER_TOKEN (Bearer) | keystore Secret `authToken` (Step B) |
| eRPC `erpc.erpc:80` | none (read-only, unauthenticated) | n/a — allowlist only |
| external APIs (named) | per-business API keys | business Secret, operator-provided |

x402 paid upstreams are mostly out of scope: those are payment-signed
per-request (the buyer sidecar / pre-signed auths), not a static secret the
proxy would hold.

## Agent CRD surface

Reuse the §C fields and add proxy opt-in:

```yaml
spec:
  models: [allowlist]          # §C — virtual key scope
  budget: {amount, period}     # §C
  egress:
    proxy: true                # inject this pod's sidecar
    allow:                     # extra L7 destinations beyond the defaults
      - host: api.example.com
        secretRef: {name: biz-keys, key: example-api}
```

Controller renders the sidecar + config when `egress.proxy: true`; the
NetworkPolicy and hermes-config rendering switch to the localhost-proxy
form. Default off so existing agents are unaffected; on for
business-template agents (see plans/agent-business-charts.md).

## Verification boundaries (per repo convention)

- "Hermes container env contains NO LiteLLM key and NO signer token when
  egress.proxy=true" (inspect rendered Deployment).
- "agent reaches LiteLLM/signer ONLY via localhost proxy; a direct call
  from the Hermes container to litellm.llm:4000 without the proxy is
  rejected/credential-less".
- "proxy denies a destination not in the allowlist".
- "streaming inference still flushes per-chunk through the proxy" (the
  Cloudflare-100s / SSE concern from CLAUDE.md).
- "compromised-agent simulation: a shell in the Hermes container cannot
  read the virtual key or signer token from any env/file/endpoint it can
  reach".

## Sequencing within the broader plan

- C1: per-agent virtual keys (budget + model allowlist + spend metrics) —
  unchanged from §C; verify hermes cap-trip behavior first.
- C2: iron-proxy spike → decide tool vs build.
- C3: egress-proxy sidecar (holds vkey + signer token, L7 allowlist,
  localhost-only), CRD `egress.proxy` opt-in, NetworkPolicy + hermes-config
  switch to localhost form.
- C2/C3 are a clean follow-on to the Step B isolation work and feed the
  business-template charts (E), which would ship with `egress.proxy: true`
  as the safe default for public-facing agents.
