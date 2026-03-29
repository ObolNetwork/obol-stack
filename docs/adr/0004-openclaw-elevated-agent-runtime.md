# ADR-0004: OpenClaw as the Elevated Agent Runtime

**Date**: 2026-03-29
**Status**: Accepted

**Impacts**: SPEC Sections 3.4, 5.2, 7.5

## Context

Obol Stack needs an automation runtime that can operate inside the cluster, consume embedded skills, and act on behalf of the operator for selected workflows. Building a separate controller family for every automation path would fragment the control model.

## Decision

The default elevated automation runtime is an OpenClaw deployment, `obol-agent`, with carefully scoped elevated permissions and embedded skills. Additional OpenClaw instances remain operator-managed deployments and do not inherit the same elevated role automatically.

## Consequences

- **Positive**: The platform reuses one agent runtime model for operator workflows and skill execution.
- **Negative**: Elevated RBAC and skill distribution must be reviewed carefully because the default agent has broader authority than ordinary instances.
- **Neutral**: New autonomous behaviors should first be expressed as skills against this runtime before introducing dedicated controllers.
