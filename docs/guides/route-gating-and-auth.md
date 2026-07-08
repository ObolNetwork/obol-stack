# Route tables: per-route pricing, free carve-outs, and wallet sign-in

By default a monetized service has exactly one gate: everything under
`/services/<name>/*` costs the offer's price. Route tables let one offer mix
**paid**, **free**, and **wallet-authenticated** routes — the shape you need
for async services (paid submit, free status page, owner-only results), for
health checks, and for per-route pricing on multi-endpoint APIs.

## Declaring routes

```bash
obol sell http audit \
  --upstream auditd --port 8080 --namespace sec \
  --pay-to 0x... --chain base --price 0.1 \
  --route "path=/submit,methods=POST,price=0.5,summary=Submit source for audit" \
  --route "path=/jobs/*,gate=free" \
  --route "path=/reports/*,gate=auth" \
  --route "path=/*,gate=paid"
```

Semantics:

- **Paths are relative to the offer prefix** (`/services/audit` here) and use
  the verifier's pattern syntax: exact (`/healthz`), greedy trailing wildcard
  (`/jobs/*`), segment globs (`/models-*/info`).
- **Declaring any route makes the table exhaustive**: undeclared paths under
  the offer prefix are not served (404 / fail-closed). Add `path=/*` for a
  catch-all.
- **More specific routes win**: an exact `/healthz` beats the offer's own
  `/*`, and `/jobs/*` beats `/*` — declaration order doesn't matter.
- `price=` overrides the offer price for that route (paid routes only). The
  override applies to the offer's primary currency; secondary
  multi-currency options are not advertised on overridden routes.
- `methods=` is discovery metadata (OpenAPI/skill.md); the gate applies to
  every method on a matched path.
- Reserved platform paths (`/api`, `/openapi.json`, `/skill.md`, `/rpc`,
  `/.well-known`, the bare `/services`, `/`) can never be claimed by an
  offer — the CLI rejects them and the controller sets
  `RoutePublished=False/ReservedPath` as a backstop.

The same table drives every surface: the payment gate, `/openapi.json`
(one operation per route with per-route `x-payment-info`), `/skill.md`, and
`/api/services.json` buy prompts.

## Gate classes

| Gate | Who gets through | Typical use |
|------|------------------|-------------|
| `paid` (default) | Valid x402 payment (`X-PAYMENT`) | The product |
| `free` | Everyone | Health checks, job status pages, per-offer discovery docs |
| `auth` | SIWX-verified wallet | Result pages bound to the buyer's wallet, member areas |

## Wallet sign-in (`gate: auth`, SIWX / EIP-4361)

An unauthenticated request to an auth route gets **`401`** with
`WWW-Authenticate: SIWX domain="<host>", window="600"` and a JSON body
describing the challenge. Browsers (`Accept: text/html`) instead get a
sign-in page: connect wallet → sign a gasless EIP-4361 message →
session cookie → redirected back to the original URL.

Three credential forms are accepted:

1. **One-shot signed message** — `Authorization: SIWX <b64 message>.<b64 signature>`,
   where the message is EIP-4361 plaintext (domain = the request host,
   `Version: 1`, fresh `Nonce`, `Issued At` within ~10 minutes) signed with
   EIP-191 `personal_sign`. Single-use.
2. **Session token** — POST `{message, signature}` to the offer's
   `/auth/verify` endpoint once, reuse the returned token as
   `Authorization: Bearer <token>` (default lifetime 24h; tokens do not
   survive a verifier restart — re-authenticate on 401).
3. **Browser cookie** — set automatically by the sign-in page at
   `<offer-prefix>/auth`.

Obol agents: the `buy-x402` skill mints headers with the agent wallet —
`buy.py siwx <url>` prints the header, `buy.py siwx <url> --fetch` performs
the request. Signing rides the remote signer; no key material is exposed.

Only EOA signatures are supported today; smart-contract wallets (EIP-1271)
are rejected with a explicit error.

## Identity headers (for upstream authors)

The verifier hands your upstream two authenticated facts — client-supplied
values are stripped, so you may trust them unconditionally:

| Header | Set on | Meaning |
|--------|--------|---------|
| `X-Payment-Payer` | paid routes | The wallet that signed the verified x402 payment |
| `X-Verified-Wallet` | auth routes | The SIWX-authenticated wallet (lowercase 0x address) |

The intended composition: bind resources you create on a paid call (job
records, reports) to `X-Payment-Payer`, and gate their retrieval routes with
`gate: auth`, authorizing when `X-Verified-Wallet` matches. The wallet that
paid is the wallet that may read — no accounts, no API keys.

## Dedicated origins (`--hostname`)

The x402 market is origin-keyed: crawlers like x402scan and agentcash group
resources per origin, so multiple offers on one shared domain list as one
mixed product. Give an offer its own origin:

```bash
obol sell http audit ... --hostname audit.example.com
# or bind later:
obol tunnel hostname add audit.example.com --offer sec/audit
```

What you get on `https://audit.example.com`:

- The offer's routes rooted at `/` (`POST /submit`, `GET /jobs/<id>`, …) —
  internally rewritten onto the same payment gate, so gates, prices, and
  SIWX all behave identically. The `/services/audit/*` path keeps working
  as an alias.
- An offer-scoped `/openapi.json` (servers = the origin, only this offer's
  operations), a `/.well-known/x402` resource list with signable payment
  requirements, and a branded landing page at `/`.
- SIWX sign-in at `/auth`; the EIP-4361 domain to sign is the offer
  hostname.

One offer per origin (first claimant wins — the CLI preflights and the
controller sets `RoutePublished=False/HostnameConflict`). The shared
storefront catch-all automatically skips offer-bound hostnames. DNS routing
to the tunnel stays your job (`obol tunnel hostname add` handles
local-managed tunnels end-to-end; dashboard-managed tunnels print the one
remaining Cloudflare step).

## Error pages and operator contact

Human-facing errors (404/403/5xx, the 401 challenge, the 402 paywall) render
branded HTML pointing buyers at the storefront and at the operator contact
published in `/openapi.json`'s `info.contact`. Set the contact once:

```bash
obol sell info set --contact-email ops@example.com
```
