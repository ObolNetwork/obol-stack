# {Project Name} Technical Specification

**Version**: 1.0.0-draft
**Status**: Draft
**Last Updated**: {date}

---

## Table of Contents

<!-- Adjust sections to fit the project. Remove irrelevant sections. Add domain-specific ones. -->

1. [Introduction](#1-introduction)
2. [System Architecture](#2-system-architecture)
3. [Core Subsystems](#3-core-subsystems)
4. [API / Protocol Definition](#4-api--protocol-definition)
5. [Data Model](#5-data-model)
6. [Integration Points](#6-integration-points)
7. [Security Model](#7-security-model)
8. [Error Handling](#8-error-handling)
9. [Performance](#9-performance)
10. [Phased Rollout](#10-phased-rollout)
11. [Testing Strategy](#11-testing-strategy)

---

## 1. Introduction

### 1.1 Purpose

<!-- One paragraph: what this document is and who it's for. -->

This document is the authoritative technical specification for {project name}. It defines every subsystem, protocol, interface, and behavioral contract that the implementation must satisfy.

### 1.2 Scope

<!-- What the system does (bullet list) and what it explicitly does NOT do (bullet list). -->

The system:
- {capability 1}
- {capability 2}
- ...

The system does **not**:
- {anti-scope 1}
- {anti-scope 2}
- ...

### 1.3 Terminology & Glossary

<!-- Every domain-specific term used in the spec. -->

| Term | Definition |
|------|-----------|
| **{Term}** | {Definition} |

### 1.4 System Constraints

<!-- Hard constraints that shape every architectural decision. -->

| Constraint | Detail |
|-----------|--------|
| **{Constraint}** | {Detail and impact} |

---

## 2. System Architecture

### 2.1 High-Level Component Diagram

<!-- ASCII or reference to ARCHITECTURE.md for Mermaid diagrams. Show the major components and how they connect. -->

### 2.2 Module Decomposition

| Module | Purpose | Key Dependencies |
|--------|---------|-----------------|
| {module} | {purpose} | {deps} |

### 2.3 Request/Response Lifecycle

<!-- Walk through a typical request from entry to response. Name every component it touches. -->

---

## 3. Core Subsystems

<!-- One subsection per major subsystem. Each should cover:
     - Purpose
     - Inputs / Outputs
     - Internal logic (pseudocode acceptable)
     - Configuration
     - Error states
-->

### 3.1 {Subsystem A}

#### 3.1.1 Purpose
#### 3.1.2 Inputs & Outputs
#### 3.1.3 Logic
#### 3.1.4 Configuration
#### 3.1.5 Error States

### 3.2 {Subsystem B}

<!-- Repeat pattern -->

---

## 4. API / Protocol Definition

<!-- Define every external-facing interface: REST endpoints, RPC methods, message formats, CLI commands, etc. -->

### 4.1 {Interface Type} (e.g., REST API, gRPC, CLI)

| Endpoint / Command | Method | Description | Request | Response |
|-------------------|--------|-------------|---------|----------|
| {endpoint} | {method} | {desc} | {schema} | {schema} |

### 4.2 Authentication & Authorization

<!-- How callers identify themselves. Token formats, key-based auth, API keys, etc. -->

### 4.3 Rate Limiting & Quotas

---

## 5. Data Model

### 5.1 Storage Architecture

<!-- What databases/stores are used, why, and what goes where. -->

### 5.2 Schema Definitions

<!-- Table schemas, document structures, key-value patterns. -->

### 5.3 Data Lifecycle

<!-- How data is created, updated, archived, and deleted. Retention policies. -->

---

## 6. Integration Points

<!-- Every external system the project talks to. -->

| System | Protocol | Purpose | Failure Mode |
|--------|----------|---------|-------------|
| {system} | {protocol} | {purpose} | {what happens when it's down} |

---

## 7. Security Model

### 7.1 Threat Model

<!-- What are we defending against? -->

### 7.2 Authentication

### 7.3 Input Validation & Sanitization

### 7.4 Data Protection

---

## 8. Error Handling

### 8.1 Error Categories

| Category | Example | Handling |
|----------|---------|---------|
| {category} | {example} | {how it's handled} |

### 8.2 Error Response Format

### 8.3 Retry & Recovery

---

## 9. Performance

### 9.1 Targets

| Metric | Target | Measurement |
|--------|--------|-------------|
| {metric} | {target} | {how measured} |

### 9.2 Bottlenecks & Mitigations

---

## 10. Phased Rollout

<!-- If applicable. Remove if single-phase delivery. -->

### Phase 1: {name}
- Scope: {what's included}
- Success criteria: {how to know it's done}

### Phase 2: {name}
- Scope: ...
- Depends on: Phase 1 completion

---

## 11. Testing Strategy

### 11.1 Test Levels

| Level | Tool | What It Covers |
|-------|------|---------------|
| Unit | {framework} | Individual functions/methods |
| Integration | {framework} | Subsystem interactions |
| BDD | Cucumber/Gherkin | Behavioral scenarios from B&E doc |
| Performance | {tool} | Latency, throughput targets |

### 11.2 Test Data Strategy

### 11.3 CI/CD Integration
