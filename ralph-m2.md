# Ralph M2: Validate Autoresearch Worker + Coordinator

Read /tmp/m1-findings.md for context from M1.
Load skills: obol-stack-dev, agentic-economy.

## The User Journey
1. Deploy autoresearch-worker as ServiceOffer:
   `obol sell http my-worker --upstream worker-api --port 8000 --namespace llm --per-request 0.01 --wallet 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 --chain base-sepolia --register --register-name GPU-Worker`
2. Wait for heartbeat → ServiceOffer Ready
3. Verify 402 at /services/my-worker via curl through Traefik
4. Verify .well-known/agent-registration.json advertises the worker
5. Run discovery.py search → finds registered worker on-chain
6. Run coordinator.py probe → gets x402 pricing from discovered worker

Use model qwen3.5:9b (NOT qwen3:0.6b).
GPU training not required — validate the HTTP flow and discovery, not training.

## Rules
- Use built binary (`obol`), not `go run`
- Wait for real heartbeat, don't exec monetize.py directly
- Route through Traefik, not pod IPs
- Commit fixes with prefix `fix(m2):`
- Write findings to `/tmp/m2-findings.md`
