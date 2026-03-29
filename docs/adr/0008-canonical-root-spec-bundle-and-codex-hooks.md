# ADR-0008: Canonical Root-Level Spec Bundle with Codex Hook Guardrails

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 10, 11.3 and CONTRIBUTING.md

## Context

The repository accumulated parallel plan files, stale design notes, and an incorrect `docs/specs/` bundle that drifted from both the code and the original backend-service-spec-bundler design. The project needed one canonical spec location and a lightweight mechanism to catch future drift during development.

## Decision

The repository follows the original backend-service-spec-bundler layout at repo root: `SPEC.md`, `ARCHITECTURE.md`, `BEHAVIORS_AND_EXPECTATIONS.md`, `CONTRIBUTING.md`, `features/`, and `docs/adr/`. Codex hooks are added as guardrails to remind the model of these conventions and to flag spec-impacting code changes when the canonical bundle was not updated in the same turn.

## Consequences

- **Positive**: The bundle has one authoritative location and drift becomes easier to detect.
- **Negative**: Contributors must maintain the canonical docs alongside behavior changes instead of relying on scattered planning notes.
- **Neutral**: Hooks assist developer workflow, but CI and human review still remain the final enforcement layer.
