# Obol Stack Front End

Next.js dashboard for the Obol Stack: network monitoring, AI agents, x402 marketplace (buy/sell), and operator wallets.

## Quick start

```bash
git clone https://github.com/ObolNetwork/obol-stack-front-end.git
cd obol-stack-front-end
pnpm install
pnpm run dev:local    # http://localhost:3000
```

With a live cluster and `obol.stack` in `/etc/hosts`, use `pnpm run dev` → `http://obol.stack:3000`.

## Features

- Ethereum / network monitoring (Prometheus + Kubernetes)
- Hermes agent + sub-agents, sell listings, ERC-8004 registration view
- Marketplace: browse services, `obol buy inference` / sell flows
- Wallet connection (RainbowKit / WalletConnect)
- CopilotKit sidebar when an ADK agent is configured

## Documentation

| Doc                                          | Contents                                     |
| -------------------------------------------- | -------------------------------------------- |
| [CONTRIBUTING.md](CONTRIBUTING.md)           | **Start here for PRs** — setup, CI, security |
| [DEVELOPMENT.md](DEVELOPMENT.md)             | Skaffold / Kind deployment                   |
| [CLAUDE.md](CLAUDE.md)                       | Agent / IDE conventions                      |
| [deployment/README.md](deployment/README.md) | Production Docker / K8s                      |
| [QUICKSTART_AGENT.md](QUICKSTART_AGENT.md)   | ADK agent hookup                             |

## Scripts

```bash
pnpm run lint && pnpm run test && pnpm run build
pnpm run test:e2e:chromium
bash scripts/scan-git-secrets.sh
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security issues: **security@obol.tech** ([policy](.github/SECURITY.md)).

## License

Apache 2.0 — see [LICENSE](LICENSE).

## Links

- [Obol Network](https://obol.org) · [Docs](https://docs.obol.org) · [Discord](https://discord.obol.org)
- [Issue tracker](https://github.com/ObolNetwork/obol-stack-front-end/issues)
