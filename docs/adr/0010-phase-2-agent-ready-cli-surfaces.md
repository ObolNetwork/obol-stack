# ADR-0010: Phase 2 Agent-Ready CLI Surfaces

**Date**: 2026-03-29
**Status**: Proposed

**Impacts**: SPEC Sections 1.3, 4.1, 10

## Context

The platform is increasingly consumed by agents as well as human operators. Human-first CLI ergonomics are still primary, but the repository also contains future-work notes for structured JSON output, headless prompt handling, and richer introspection.

## Decision

Phase 2 agent-facing improvements should add structured output, non-interactive input paths, and machine-friendly introspection without replacing the human-first operator contract. The local operator remains the primary actor, so agent-ready surfaces are an extension of the CLI rather than a separate control plane by default.

## Consequences

- **Positive**: Agents and future MCP adapters gain a safer path to consume the CLI without scraping human output.
- **Negative**: Every new machine-facing surface must preserve compatibility with existing operator workflows and documentation.
- **Neutral**: A dedicated MCP layer remains optional and should be introduced only if the structured CLI surface proves insufficient.
