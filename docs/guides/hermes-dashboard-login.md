# Hermes dashboard login (basic-auth)

Local Hermes dashboards are gated with password basic-auth after hermes-agent
v2026.7.x.

## Working URL (pretty host)

```
http://obol-agent.obol.stack
```

Named instances use `http://hermes-<id>-ui.obol.stack`.

Obol’s dashboard `HTTPRoute` **edge-redirects** Exact `/` → `/auth/password-login`
(302) so you never have to type the form path. The agent API host
(`hermes-obol-agent.obol.stack`) is unchanged.

| Field | Value |
|-------|--------|
| Username | `obol` |
| Password | Agent API token from `obol agent auth <id>` |

The password is the same secret Obol wires as `HERMES_DASHBOARD_BASIC_AUTH_PASSWORD`
(and `API_SERVER_KEY`). It never leaves your machine (`obol.stack` → loopback).

CLI alternative (no browser): `obol hermes chat`.

## Why the redirect exists

Hermes’s own root handling is broken for password-only basic-auth:

```
GET /  (if proxied straight to Hermes)
  → Hermes redirects to /auth/login?provider=basic
  → NotImplementedError: BasicAuthProvider is password-only
  → HTTP 500
```

The real form lives at `/auth/password-login`. Obol intercepts Exact `/` on the
**dashboard hostname only** and issues:

```
302 Location: /auth/password-login
```

So operators open the pretty host; Traefik never forwards bare `/` into Hermes’s
broken auto-login path.

| Layer | Ownership |
|-------|-----------|
| Upstream Hermes (`nousresearch/hermes-agent`) | Ideally fix `/` (and login CTAs) to land on `/auth/password-login` when basic-auth is password-only |
| Obol Stack | Edge-redirect dashboard `/` → password form; print pretty URL + credentials after install/sync; smoke-test root + password-login non-500 in flow-04 |

Fallback if the edge rule is missing (old install before re-sync): open
`http://obol-agent.obol.stack/auth/password-login` directly, then
`obol agent sync` / re-install to pick up the redirect.
