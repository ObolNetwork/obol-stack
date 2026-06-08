# Storefront CTA handoff: `obol buy inference` for paid-inference offers

Sibling repo: `ObolNetwork/obol-stack-front-end` (path env: `OBOL_FRONTEND_DIR`).

## What we just shipped in obol-stack

The `obol buy inference` CLI was reshaped (see `cmd/obol/buy.go`,
`internal/buy/discover.go`, `internal/x402/paymentrequired.go`):

- Positional seller URL: `obol buy inference [<seller-url>]`. With no URL,
  the public Obol storefront (`https://inference.v1337.org/`) is used.
- `--model`, `--budget`, and identity verification are now optional. The
  CLI walks `/api/services.json`, prefers `type=inference` offers, and
  resolves model + token from the catalog entry.
- TTY interactive prompts for auto-refill, request count, and a final
  confirmation. `-y`/`--yes` for headless runs.
- `--agent X` pays from X's wallet AND switches X's hermes config to
  `paid/<model>` (per-agent, in-pod). `--set-default` promotes the model
  to head-of-list and syncs every agent (global).

The HTTP 402 HTML page (the gate buyers see when their browser hits a
paid `/services/<name>` URL) was updated for `type=inference` offers
only. New copy:

- **Lede**: "Your Obol agent runs locally; the model itself runs on the
  remote operator's hardware and is gated by x402 micropayments. The
  CLI below pre-pays the provider through your agent's wallet and
  registers the model as `paid/<model>` in your local LiteLLM gateway."
- **Primary CTA** ("Use this service for your Obol Agent's model"):
  copyable one-liner `obol buy inference <seller-url>`.
- **Obol-agent prompt card**: rephrased to instruct the receiving agent
  to explain the service, offer to buy, and switch itself over to
  `paid/<model>` after the buy lands.

## What the storefront should mirror

The `/services/<name>` detail page rendered by the Next.js storefront
is the *non-402* parallel to the 402 HTML page — it's what someone sees
when browsing the storefront listing rather than trying to use the
endpoint. For `type=inference` entries (`ServiceCatalogEntry.type ===
"inference"`), it should render the same conceptual CTA:

1. **Headline tile**: "Use this service for your Obol Agent's model"
   with a `<pre>` block:
   ```
   obol buy inference <seller-url>
   ```
   where `<seller-url>` is `${siteOrigin}${entry.endpoint}` (strip any
   `/v1/chat/completions` suffix the controller emits).

2. **Subtext** explaining the local-agent / remote-model split — same
   prose as the 402 lede, suitable for an unauthenticated visitor. Keep
   it short (≤2 sentences).

3. **"Pre-pays via your agent's wallet"** line with a small `(?)` that
   links to the `buy-x402` skill in obol-docs for buyers who want the
   deep-dive.

4. **Pricing display**: when `entry.priceUnit === "perMTok"`, show
   "Up to $X per request (≈1k tokens)" instead of a flat per-request
   number. x402 settles at actual usage, so unused capacity isn't
   charged — call this out in a one-line note. Source of truth for the
   per-unit display is `entry.price` + `entry.priceUnit`.

5. **Type filter**: do not surface the inference CTA on `type=agent` or
   `type=http` cards. Those keep their existing prompts (agents need
   payload context, http is single-shot).

6. **Operator description**: continue to use `entry.description` — no
   change needed.

## Wire format reference

The catalog entries the storefront already consumes are typed in
`internal/schemas/service_catalog.go` (kept in obol-stack). Relevant
fields:

```ts
type ServiceCatalogEntry = {
  name: string;
  type: "inference" | "agent" | "http" | "fine-tuning";
  model?: string;
  endpoint: string;         // path-only, e.g. "/services/aeon"
  price: string;            // human display, e.g. "0.01"
  priceUnit?: "perRequest" | "perMTok" | "perHour";
  priceAtomicUnits?: string;
  payTo: string;
  network: string;
  caip2Network?: string;
  asset?: { address, symbol, decimals, transferMethod, eip712Domain };
  description: string;
  skills?: string[];
  isDemo: boolean;
  drainEndsAt?: string;     // RFC3339; ignore if absent
};
```

Schema is stable; this PR doesn't add or remove fields.

## Out of scope for the handoff

- ERC-8004 identity badge — the obol-stack CLI made identity verification
  opt-in (default skipped), so the storefront should not gate the CTA
  on registration status.
- Drain banners — existing logic for `drainEndsAt` is unaffected.
- Agent-marketplace embedding — separate effort.

## Verification

After implementing:

1. Hit `https://inference.v1337.org/` and confirm at least one card
   advertising `type: inference` renders the new CTA.
2. Click into a card and confirm the copy block matches what
   `internal/x402/paymentrequired.go::inferenceCopy` produces for the
   same offer (modulo the host-vs-404 framing).
3. Verify the CTA URL is `${origin}/services/<name>` (no
   `/v1/chat/completions` suffix). `obol buy inference` accepts both
   shapes but the cleaner form is the route base.
