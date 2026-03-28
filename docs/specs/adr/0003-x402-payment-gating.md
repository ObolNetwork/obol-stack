# ADR-0003: x402 Payment Gating for Services

**Status:** Accepted
**Date:** 2026-03-27

## Context

Obol Stack needs a mechanism for operators to monetize cluster services (inference, HTTP endpoints). The payment system must be:

- **Permissionless**: No API key registration, no account creation, no subscription management.
- **Per-request**: Each request is independently priced and paid for.
- **Gasless for buyers**: Buyers should not need to pay blockchain gas for every inference request.
- **Machine-to-machine**: AI agents must be able to pay autonomously without human interaction.
- **Composable**: Payment gating should work with any HTTP service behind Traefik, not just inference.

Alternatives considered:

| Option | Pros | Cons |
|--------|------|------|
| **x402 (HTTP 402)** | Permissionless, per-request, gasless via ERC-3009, standard HTTP, agent-native | Facilitator dependency, USDC-only, limited chain support |
| **API keys** | Simple, widely understood | Requires user registration, key management, not agent-native |
| **Stripe/subscriptions** | Established, fiat currency | Requires merchant account, not permissionless, not agent-to-agent |
| **Lightning Network** | Per-request micropayments, mature | Bitcoin-only, requires channel management, different user base |
| **State channels** | Low latency, off-chain | Complex setup, requires both parties online, custom protocol |

## Decision

Use the **x402 protocol** (HTTP 402 Payment Required) with **ERC-3009** (TransferWithAuthorization) for gasless USDC micropayments, implemented via **Traefik ForwardAuth** middleware.

## Rationale

1. **HTTP-native**: x402 uses standard HTTP 402 status codes. Any HTTP client can discover pricing by making an unauthenticated request. Payment is attached as an `X-PAYMENT` header.
2. **Gasless for buyers**: ERC-3009 `TransferWithAuthorization` allows pre-signed USDC transfers. The buyer signs once; the facilitator settles on-chain. No gas from the buyer.
3. **Traefik ForwardAuth**: The x402-verifier runs as a ForwardAuth middleware. Every request matching a Middleware is sent to `POST /verify`. This cleanly separates payment from business logic -- the upstream service never sees payment details.
4. **Facilitator delegation**: Payment verification and settlement are delegated to a trusted facilitator (`https://facilitator.x402.rs`). This simplifies the verifier to a stateless proxy.
5. **Multi-chain support**: The system supports Base, Polygon, and Avalanche (mainnet + testnet). Chain configuration is per-route.
6. **Agent-native**: AI agents can programmatically discover pricing (402 response), sign payments (ERC-3009), and consume services without human intervention.

## Consequences

### Positive

- Any HTTP service can be monetized by adding a ServiceOffer CR -- no code changes to the upstream.
- Agents discover pricing automatically via the 402 response.
- USDC stablecoin avoids cryptocurrency price volatility.
- The ForwardAuth pattern means payment logic is fully decoupled from service logic.
- Route-level pricing: different paths can have different prices, wallets, and chains.

### Negative

- **Facilitator dependency**: Payment verification requires the facilitator to be reachable. If `facilitator.x402.rs` is down, all paid requests fail. No offline fallback exists.
- **USDC-only**: Only USDC is supported as the payment asset. Other stablecoins or tokens require facilitator support.
- **Limited chain support**: Only 6 chains (3 mainnets + 3 testnets) are supported. Adding new chains requires code changes to the chain resolution logic.
- **Phase 1 pricing approximation**: `perMTok` pricing is approximated as `perMTok / 1000` (fixed 1000 tokens per request). Exact token metering is deferred to phase 2.
- **HTTPS requirement**: The facilitator URL must use HTTPS (loopback exempted for testing). This prevents local-only facilitator setups without TLS.
- **Settlement latency**: Facilitator verification adds 100-500ms per request. This is acceptable for inference but may be too slow for high-frequency API calls.

## SPEC References

- Section 3.4 -- Monetize Sell Side
- Section 4.1 -- x402 Payment Protocol
- Section 3.4.4 -- x402-verifier (ForwardAuth)
- Section 3.4.5 -- Pricing
- Section 3.4.6 -- Supported Chains
- Section 7.2 -- Payment Security
