# Block Explorer URL Templates

Copy-pasteable links for handing to a human who needs to verify an address or
transaction in a browser. Substitute `{address}` (0x + 40 hex) or `{tx}`
(0x + 64 hex). URL patterns lifted from swiss-knife.xyz's explorer registry.

The scripts in this skill are advisory; these explorers are where a human
cross-checks before approving a signature or payment.

## Address / Contract Explorers

| Explorer | URL template | Chains | Notes |
|----------|-------------|--------|-------|
| Etherscan | `https://etherscan.io/address/{address}` | mainnet | Canonical: txs, verified source, proxy read/write tabs |
| Etherscan (Sepolia) | `https://sepolia.etherscan.io/address/{address}` | sepolia | |
| Basescan | `https://basescan.org/address/{address}` | base | |
| Basescan (Sepolia) | `https://sepolia.basescan.org/address/{address}` | base-sepolia | |
| Blockscout | `https://eth.blockscout.com/address/{address}` | mainnet (`eth.`), base (`base.`), optimism (`optimism.`) | Subdomain per chain; open-source alternative to Etherscan |
| Sourcify repo | `https://repo.sourcify.dev/{chainId}/{address}` | any EVM chain (numeric chain id: 1, 8453, 84532, 11155111) | Raw verified-source tree; same backend `contract.py source` queries |
| Tenderly | `https://dashboard.tenderly.co/contract/{chain}/{address}` | mainnet, sepolia, optimistic, polygon, ... | Chain labels: `mainnet`, `sepolia` (no Base in this view) |
| Dedaub | `https://library.dedaub.com/{chain}/address/{address}` | ethereum, base, arbitrum, fantom | Decompiler — readable code for UNVERIFIED contracts |
| evm.codes | `https://www.evm.codes/contract?address={address}` | mainnet | Runtime bytecode disassembly view |
| UpgradeHub | `https://upgradehub.xyz/diffs/etherscan/{address}` | mainnet (label `etherscan`; also `arbiscan`, `polygonscan`, `optimistic.etherscan`, ...) | Diffs every proxy implementation upgrade — great after `contract.py proxy` |
| OpenSea | `https://opensea.io/{address}` | mainnet, base, optimism, polygon, ... | NFTs held by the address |
| OpenSea (testnets) | `https://testnets.opensea.io/{address}` | sepolia + other testnets | |

## Transaction Explorers

| Explorer | URL template | Chains | Notes |
|----------|-------------|--------|-------|
| Etherscan | `https://etherscan.io/tx/{tx}` | mainnet | |
| Etherscan (Sepolia) | `https://sepolia.etherscan.io/tx/{tx}` | sepolia | |
| Basescan | `https://basescan.org/tx/{tx}` | base | |
| Basescan (Sepolia) | `https://sepolia.basescan.org/tx/{tx}` | base-sepolia | |
| Blockscout | `https://eth.blockscout.com/tx/{tx}` | mainnet (`eth.`), base (`base.`), optimism (`optimism.`) | |
| Tenderly | `https://dashboard.tenderly.co/tx/{chain}/{tx}` | `mainnet`, `sepolia`, `optimistic`, `polygon`, ... | Full execution trace + state diff — best "what did this tx really do" view |
| Phalcon | `https://explorer.phalcon.xyz/tx/{chain}/{tx}` | `eth`, `sepolia`, `optimism`, `polygon`, ... | Security-oriented trace explorer (fund flow, call tree) |
| EigenPhi | `https://tx.eigenphi.io/analyseTransaction?chain=ALL&tx={tx}` | mainnet + majors | MEV / sandwich analysis |

## Chain quick reference

| eRPC network alias | Chain id | Primary explorer |
|--------------------|----------|------------------|
| `mainnet` | 1 | etherscan.io |
| `base` | 8453 | basescan.org |
| `base-sepolia` | 84532 | sepolia.basescan.org |
| `sepolia` | 11155111 | sepolia.etherscan.io |
| `hoodi` | 560048 | hoodi.etherscan.io (limited third-party coverage) |
