# Agent chat widget assets

`chat.html` — the self-contained agent chat page served at `/chat` on every
agent-type offer's dedicated origin. Hand-maintained; no build step. It
derives the agent name from the hostname and price/model/network/asset from
the live 402 challenge, so the same file works for every agent offer on any
stack and network (Base mainnet `eip155:8453` and Base Sepolia
`eip155:84532` are supported).

`chat-vendor.js` — generated single-file ESM bundle of the widget's
dependencies. Do not edit by hand. Rebuild:

```sh
npm init -y && npm i viem@2.21.25 @x402/fetch@2.18.0 @x402/evm@2.18.0
cat > vendor-entry.mjs <<'EOF'
export { createWalletClient, createPublicClient, custom, http, erc20Abi,
         formatUnits, parseUnits, keccak256 } from "viem";
export { privateKeyToAccount } from "viem/accounts";
export { base, baseSepolia } from "viem/chains";
export { wrapFetchWithPayment, x402Client } from "@x402/fetch";
export { ExactEvmScheme, toClientEvmSigner } from "@x402/evm";
EOF
npx esbuild vendor-entry.mjs --bundle --format=esm --minify --target=es2022 \
  --outfile=chat-vendor.js
```

sha256 of the committed bundle:
`895fd923aa84d7cf80e2b1df299068aa38dba7307a9a380526c0b5426489724d`

The pinned versions are the exact pair validated end-to-end against the
x402-verifier with real on-chain settlements (X-PAYMENT v1 and
PAYMENT-SIGNATURE v2 flows both accepted since #690).
