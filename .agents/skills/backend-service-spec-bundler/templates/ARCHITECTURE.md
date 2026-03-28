# {Project Name} Architecture

**Version**: 1.0.0-draft
**Status**: Draft
**Last Updated**: {date}

This document provides a visual and structural overview of the {project name} system. For the full technical specification, see [SPEC.md](SPEC.md).

---

## Table of Contents

1. [System Overview](#1-system-overview)
2. [Component Diagrams](#2-component-diagrams)
3. [Module Decomposition](#3-module-decomposition)
4. [Data Flow Diagrams](#4-data-flow-diagrams)
5. [Storage Architecture](#5-storage-architecture)
6. [Deployment Model](#6-deployment-model)
7. [Network Topology](#7-network-topology)
8. [Security Architecture](#8-security-architecture)

---

## 1. System Overview

### Design Philosophy

<!-- 3-5 principles that drove every architectural decision. -->

{Project name} is built around these principles:

1. **{Principle 1}**: {explanation}
2. **{Principle 2}**: {explanation}
3. **{Principle 3}**: {explanation}

### System Constraints

| Constraint | Impact on Architecture |
|-----------|----------------------|
| {constraint} | {how it shapes the design} |

---

## 2. Component Diagrams

### 2.1 C4 Context Diagram

Shows {project name} in relation to all external systems.

```mermaid
C4Context
    title {Project Name} — System Context

    Person(user, "{User Role}", "{User description}")

    System(sys, "{Project Name}", "{One-line description}")

    System_Ext(ext1, "{External System 1}", "{Description}")
    System_Ext(ext2, "{External System 2}", "{Description}")

    Rel(user, sys, "{interaction}")
    Rel(sys, ext1, "{protocol}", "{what flows}")
    Rel(sys, ext2, "{protocol}", "{what flows}")
```

### 2.2 C4 Container Diagram

Zooms into {project name} to show its internal containers.

```mermaid
C4Container
    title {Project Name} — Container Diagram

    Container_Boundary(boundary, "{Project Name}") {
        Container(comp1, "{Component 1}", "{Tech}", "{Purpose}")
        Container(comp2, "{Component 2}", "{Tech}", "{Purpose}")
        ContainerDb(db, "{Database}", "{Type}", "{What it stores}")
    }

    System_Ext(ext1, "{External System}", "{Description}")

    Rel(comp1, comp2, "{interaction}")
    Rel(comp2, db, "{protocol}")
    Rel(comp1, ext1, "{protocol}")
```

### 2.3 C4 Component Diagram (optional — for complex subsystems)

```mermaid
C4Component
    title {Subsystem Name} — Component Diagram

    Component(c1, "{Component}", "{Tech}", "{Purpose}")
    Component(c2, "{Component}", "{Tech}", "{Purpose}")

    Rel(c1, c2, "{interaction}")
```

---

## 3. Module Decomposition

| Module | Purpose | Key Dependencies | SPEC Reference |
|--------|---------|-----------------|----------------|
| {module} | {purpose} | {deps} | Section {N} |

---

## 4. Data Flow Diagrams

### 4.1 {Primary Flow} (e.g., "User Request Lifecycle")

```mermaid
sequenceDiagram
    participant U as User
    participant A as {Component A}
    participant B as {Component B}
    participant E as {External System}

    U->>A: {action}
    A->>B: {internal call}
    B->>E: {external call}
    E-->>B: {response}
    B-->>A: {result}
    A-->>U: {response}
```

### 4.2 {Secondary Flow}

<!-- Add as many flows as needed to cover the major paths through the system. -->

---

## 5. Storage Architecture

### 5.1 Overview

<!-- Which stores exist, what they hold, why they were chosen. -->

### 5.2 Schema Summary

<!-- Key tables/collections/buckets — not full DDL, just enough to understand the data model. Full schema is in SPEC.md. -->

| Store | Entity | Key Fields | Purpose |
|-------|--------|-----------|---------|
| {store} | {entity} | {fields} | {purpose} |

---

## 6. Deployment Model

### 6.1 Deployment Diagram

```mermaid
graph TD
    subgraph "{Environment}"
        A["{Component}"] --> B["{Component}"]
        B --> C["{Store}"]
    end
```

### 6.2 Infrastructure Requirements

| Resource | Requirement | Notes |
|----------|-------------|-------|
| {resource} | {spec} | {notes} |

---

## 7. Network Topology

<!-- How components communicate. Protocols, ports, TLS, etc. -->

---

## 8. Security Architecture

### 8.1 Trust Boundaries

<!-- Where trust boundaries exist in the system. What crosses them. -->

### 8.2 Authentication Flow

```mermaid
sequenceDiagram
    participant C as Client
    participant S as Server
    participant A as Auth Provider

    C->>S: Request + credentials
    S->>A: Validate
    A-->>S: Token/result
    S-->>C: Authenticated response
```

### 8.3 Data Encryption

| Data | At Rest | In Transit |
|------|---------|-----------|
| {data type} | {method} | {method} |
