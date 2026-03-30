# Ralph M3: Validate Indexing Serving

Read /tmp/m1-findings.md and /tmp/m2-findings.md for context.
Load skills: obol-stack-dev.

## The User Journey
1. Build reth indexer: `cargo build --release` in reth-erc8004-indexer/
2. Verify 8004scan API: GET /api/v1/public/agents returns agent list
3. Deploy as ServiceOffer:
   `obol sell http my-indexer --upstream indexer --port 8080 --namespace llm --per-request 0.0001 --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 --chain base-sepolia`
4. Verify 402 at /services/my-indexer via Traefik
5. Verify coordinator fallback: internal indexer → external 8004scan

If reth doesn't build cleanly, document the gap and defer.
Use model qwen3.5:9b.

## Rules
- Commit fixes with prefix `fix(m3):`
- Write findings to `/tmp/m3-findings.md`
