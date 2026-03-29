# Spec Bundle Methodology Reference

This document describes the specification-first, behavior-driven methodology used by the
specbundle skill. Claude should internalize these principles when generating spec bundles.

---

## Core Philosophy

**Specs are the product, not the code.** A well-written spec bundle should allow any
competent engineer — or an AI coding agent — to implement the system without asking
clarifying questions. If the implementer needs to guess, the spec failed.

**Simplicity is the default.** Every piece of infrastructure in the spec must justify its
existence. The question is never "why not use Postgres?" — it's "why not SQLite?" or "why
not the filesystem?" Start from the leanest possible architecture (single process, embedded
storage, in-memory state) and only add complexity when the requirements demand it. A system
that runs as a single binary with zero external dependencies is not "toy" — it's the ideal
starting point. Complexity is added, never assumed.

**No comfort-zone defaults.** This methodology targets experienced developers who value
type safety, performance, and correctness. Do not default to JavaScript, Python, or JSON
because they're popular. Treat the full spectrum of languages (Rust, Go, Java, Kotlin, C++,
Zig, C#) and serialization formats (SBE, FlatBuffers, Protobuf, Cap'n Proto, MessagePack,
CBOR) as first-class options. The right choice comes from the project's constraints, not
from what's most common on Stack Overflow.

## The Three-Tier Model

Every component in the system gets three documents that form a layered specification:

### Tier 1: Technical Specification (SPEC.md)

The authoritative blueprint. Contains:
- System scope and anti-scope
- Terminology glossary (every domain term defined)
- System constraints table (hard limits that shape all decisions)
- Module decomposition with dependencies
- Complete API/protocol definitions
- Data model and storage architecture
- Security model
- Error handling taxonomy
- Performance targets
- Phased rollout plan
- Testing strategy

**Key quality test**: Can someone implement from this document alone? If no, it's incomplete.

### Tier 2: Architecture Document (ARCHITECTURE.md)

The visual/structural companion. Contains:
- Design philosophy (3-5 guiding principles)
- C4 diagrams (Context, Container, Component) in Mermaid
- Module decomposition table with SPEC cross-references
- Sequence diagrams for major data flows
- Storage architecture overview
- Deployment model diagram
- Network topology
- Security architecture (trust boundaries, auth flows)

**Key quality test**: Can someone understand the system structure in 10 minutes from diagrams alone?

### Tier 3: Behavioral Contract (BEHAVIORS_AND_EXPECTATIONS.md)

The testable behavioral specification. Contains:
- Desired behaviors: Trigger → Expected → Rationale
- Undesired behaviors: Trigger → Expected → Risk
- Edge cases: Scenario → Expected Handling → Rationale
- Performance expectations with targets and degradation handling
- Guardrail definitions (non-negotiable constraints)

**Key quality test**: Can every entry be expressed as a Gherkin scenario?

## BDD Feature Files

One `.feature` file per major feature area. Each file:
- Uses `@bdd` tag at the top
- Includes a user story (As a / I want / So that)
- Has a header comment referencing SPEC and B&E sections
- Uses a Background block for common preconditions
- Tags scenarios with phase (`@phase1`, `@phase2`) and speed (`@fast`)
- Each scenario traces back to a specific B&E entry

**Naming convention**: `{feature-area}.feature` (e.g., `trading.feature`, `authentication.feature`)

## Architecture Decision Records (ADRs)

One ADR per significant architectural decision. Each ADR:
- Is numbered sequentially: `0001-`, `0002-`, etc.
- Has three sections: Context, Decision, Consequences
- Is immutable once accepted (supersede, don't edit)
- Lives in `docs/adr/`

**When to write an ADR**: Technology choices, protocol decisions, deployment model, database
selection, major pattern decisions, constraint trade-offs.

## Cross-Reference System

All documents maintain bidirectional references:

```
SPEC.md Section 4 ←→ ARCHITECTURE.md Section 3 (Module Decomposition)
                  ←→ B&E Section 2.1 (Desired Behaviors for this subsystem)
                  ←→ features/pipeline.feature (BDD scenarios)
                  ←→ docs/adr/0003-*.md (Decision that shaped this subsystem)
```

## The Discovery Process

The spec writer's job during discovery is to extract:

1. **The problem** — What pain exists today?
2. **The actors** — Who uses this and what are their goals?
3. **The scope** — What's in and what's explicitly out?
4. **The constraints** — What's non-negotiable? (Security, compliance, performance, infra)
5. **The interfaces** — How do users and external systems interact with it?
6. **The data** — What's stored, where, and for how long?
7. **The happy paths** — Walk through the top 3-5 use cases end to end
8. **The failure modes** — What breaks and how does the system recover?
9. **The phases** — What's MVP vs. future?
10. **The decisions** — What choices were already made and why?

Good discovery questions are specific, not generic. "What's your tech stack?" is fine.
"Tell me about your requirements" is too vague.

### Challenging Complexity During Discovery

The spec writer must actively push back on unnecessary complexity. Common patterns to challenge:

| User Says | Challenge With |
|-----------|---------------|
| "We need Redis for caching" | What's the dataset size? Could an in-process LRU or SQLite handle it? |
| "Postgres for the database" | What's the data volume and query complexity? Would SQLite or embedded RocksDB suffice? |
| "Kafka for event streaming" | What's the throughput? Could an in-process queue or simple file-based log work? |
| "Microservices architecture" | What's the team size? How often do components deploy independently? Would a modular monolith be simpler? |
| "JSON REST API" | What are the payload sizes and call frequency? Would a binary format (SBE, FlatBuffers, Protobuf) be more appropriate? |
| "Docker + Kubernetes" | Could this ship as a single binary / fat JAR? What's the actual scaling requirement? |
| "Python/Node for the backend" | What are the latency and throughput requirements? Is GC pressure a concern? Would a compiled language be a better fit? |

The goal is not to reject these technologies — they're all valid in the right context. The goal
is to ensure they earn their place in the spec through concrete requirements, not habit.

## Adapting to Different Domains

The templates are derived from a crypto/DeFi project but the methodology is domain-agnostic.
When applying to other domains:

- Replace domain-specific sections with relevant ones (e.g., "Multi-Chain Support" becomes
  "Multi-Region Support" for a distributed web app)
- Adjust the constraint table for the domain (e.g., HIPAA for healthcare, PCI for payments)
- Scale the number of feature files to the project's complexity
- Adjust phasing to the project's delivery model
- Add domain-specific guardrails (e.g., "never expose PII" for a user-facing app)

## The Complexity Ladder

When specifying infrastructure, start at the bottom and move up only when a concrete
requirement forces you to:

### Storage
```
Filesystem (flat files, append-only logs)
  ↓ need indexed queries?
SQLite / DuckDB (embedded, zero-config, single-file)
  ↓ need concurrent write-heavy workloads from multiple processes?
Postgres / MySQL (server-based RDBMS)
  ↓ need horizontal write scaling or document flexibility?
Distributed stores (CockroachDB, Cassandra, MongoDB)
```

### Caching
```
In-process cache (LRU map, Caffeine, moka)
  ↓ need shared cache across multiple processes?
Redis / Memcached
  ↓ need persistence + cache semantics?
Redis with AOF / embedded KeyDB
```

### Message Passing
```
In-process channels / queues (channels, ring buffers, Disruptor)
  ↓ need cross-process or cross-machine messaging with low latency?
ZeroMQ / Aeron (brokerless, low-latency — work over IPC, TCP, and UDP/multicast)
  ↓ need built-in durability, replay, or managed routing?
NATS / RabbitMQ
  ↓ need massive throughput + log semantics + consumer groups?
Kafka / Redpanda
```

### Serialization
```
Binary schema-driven (SBE, FlatBuffers, Cap'n Proto) — zero-copy, type-safe, fast
  ↓ need schema evolution with broad ecosystem support?
Protobuf / MessagePack / CBOR — compact, good tooling
  ↓ need human readability for debugging/config?
JSON / YAML / TOML — readable, verbose, no type safety at the wire level
```

### Deployment
```
Single binary / fat JAR (zero dependencies)
  ↓ need process isolation or heterogeneous runtimes?
Containers (Docker)
  ↓ need orchestration across machines?
Kubernetes / Nomad
```

Each step up the ladder adds operational cost, failure modes, and cognitive load. The spec
must justify every step taken.

## Multiple Subsystems

Large projects may need separate spec bundles for distinct subsystems. In this case:
- Each subsystem gets its own SPEC, ARCHITECTURE, and B&E file (prefixed with the subsystem name)
- Feature files are organized per subsystem
- A top-level README ties them together
- Cross-references work across subsystem documents
