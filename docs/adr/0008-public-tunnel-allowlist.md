# ADR-0008: Public Tunnel Allowlist

**Date**: 2026-05-20
**Status**: Accepted
**Impacts**: SPEC 3.2, 5.8, 6

## Context

Cloudflare tunnel makes local services public. The stack must avoid accidentally publishing local dashboards, RPC, LiteLLM, or monitoring.

## Decision

Public tunnel traffic may reach only paid `/services/*`, `/skill.md`, `/api/services.json`, `/.well-known/agent-registration.json`, and the storefront root. Internal services remain hostname-restricted to `obol.stack`.

## Consequences

- Positive: seller endpoints and discovery are reachable by buyers.
- Positive: local control-plane surfaces stay private.
- Negative: every route template change must be reviewed as a security change.
- Neutral: quick tunnel URL churn is an operational concern handled by warnings and sync.
