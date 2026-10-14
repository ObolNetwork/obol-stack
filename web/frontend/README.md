# Obol Stack Front End

Next.js dashboard for the Obol Stack: network monitoring, AI agents, x402 marketplace (buy/sell), and operator wallets. Served locally at `http://obol.stack:8080` by `obol stack up` (image `ghcr.io/obolnetwork/obol-stack-front-end`, built from this directory).

## Quick start

```bash
cd web/frontend
corepack enable
pnpm install
pnpm run dev:local    # http://localhost:3000
```

This runs the UI standalone — no cluster needed. Dashboard and marketplace show a "Stack unreachable" state without a live cluster, but all UI, unit tests, and Playwright e2e tests work.

With a running stack (`obol stack up`), add `127.0.0.1 obol.stack` to `/etc/hosts` and use `pnpm run dev` for the full experience at `http://obol.stack:3000`, or rebuild the in-cluster image with `just dev-frontend` from the repo root (see [CLAUDE.md](CLAUDE.md)).

## Features

- Ethereum / network monitoring (Prometheus + Kubernetes)
- Hermes agent + sub-agents, sell listings, ERC-8004 registration view
- Marketplace: browse services, `obol buy inference` / sell flows
- Wallet connection (RainbowKit / WalletConnect)

## Documentation

| Doc                                | Contents                               |
| ---------------------------------- | -------------------------------------- |
| [CONTRIBUTING.md](CONTRIBUTING.md) | **Start here for PRs** — setup, checks |
| [e2e/README.md](e2e/README.md)     | Playwright mocked vs integration tiers |
| [CLAUDE.md](CLAUDE.md)             | Agent / IDE conventions, dev images    |

## Scripts

```bash
pnpm run lint && pnpm run test && pnpm run build
pnpm run test:e2e:chromium
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: **security@obol.tech**.

## License

Apache 2.0 — see the repository [LICENSE](../../LICENSE).
