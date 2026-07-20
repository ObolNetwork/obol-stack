# Agent chat widget assets

`chat.html` — the self-contained agent chat page served at `/chat` on every
agent-type offer's dedicated origin. Hand-maintained; no build step. It
derives the agent name from the hostname and price/model/network/asset from
the live 402 challenge, so the same file works for every agent offer on any
stack and network (Base mainnet `eip155:8453` and Base Sepolia
`eip155:84532` are supported).

## Known limitations

- **Session key is derived from a wallet signature** (`keccak256(personal_sign(<EIP-4361 message>))`),
  so the wallet must produce a *deterministic* signature for the same message.
  Standard RFC-6979 EOAs (MetaMask, Rabby, Ledger, Trezor) do; the `getCode`
  guard rejects contract wallets (non-reproducible ERC-1271) while allowing
  EIP-7702-delegated EOAs. **Residual gap:** MPC / threshold-ECDSA wallets are
  code-less EOAs that pass the guard but sign non-deterministically — funding a
  session from one strands the balance on the next visit. There is no on-chain
  signal to detect this without a second signature popup, which this
  one-signature flow deliberately avoids. Use a standard EOA.
- **Per-turn spend is capped at the price shown when the page loaded** (and the
  session balance). A turn whose 402 amount exceeds the displayed price is
  refused; a legitimate price change is picked up on reload. Max loss per
  session is bounded by what you fund into the session wallet.
- **The signature itself is key material.** Anything that can read it (a
  malicious extension, a hooked `window.ethereum`) controls the session funds —
  keep session balances small.

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

When the bundle is rebuilt, bump the `?v=` cache-buster on the
`chat-vendor.js` import in `chat.html` to the new sha256's first 8 hex
chars — intermediaries (e.g. Cloudflare) cache `.js` aggressively.

sha256 of the committed bundle:
`895fd923aa84d7cf80e2b1df299068aa38dba7307a9a380526c0b5426489724d`

The pinned versions are the exact pair validated end-to-end against the
x402-verifier with real on-chain settlements (X-PAYMENT v1 and
PAYMENT-SIGNATURE v2 flows both accepted since #690).
