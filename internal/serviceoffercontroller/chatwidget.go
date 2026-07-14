package serviceoffercontroller

import _ "embed"

// The agent chat widget: a self-contained browser chat client served free on
// every agent-type offer's dedicated origin at /chat (and embedded on the
// offer's landing page). The page discovers everything at runtime from its
// own origin — price, model, payment network and asset come from the 402
// challenge on POST /v1/chat/completions — so one static copy serves every
// agent offer on any stack, mainnet or testnet.
//
// Payment is fully client-side: the visitor connects an injected wallet,
// signs one fixed message ("sign in with Ethereum") whose keccak256 becomes
// a deterministic local session key, funds that session address with a small
// USDC transfer, and every chat turn is then paid silently via x402
// (EIP-3009 transferWithAuthorization signed by the session key — gasless
// for the payer). The session key never leaves the page and is re-derived by
// re-signing the same message, so nothing is persisted.
//
//go:embed assets/chat.html
var chatWidgetHTML string

// chatWidgetVendorJS is the widget's only dependency: viem 2.21.25 +
// @x402/fetch 2.18.0 + @x402/evm 2.18.0 bundled into one ESM file so the
// page loads with zero external requests (no CDN, works on air-gapped
// stacks). Rebuild (see assets/README.md):
//
//	npm i viem@2.21.25 @x402/fetch@2.18.0 @x402/evm@2.18.0
//	esbuild vendor-entry.mjs --bundle --format=esm --minify --target=es2022
//
//go:embed assets/chat-vendor.js
var chatWidgetVendorJS string
