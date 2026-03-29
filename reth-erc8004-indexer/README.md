# Reth ERC-8004 Indexer

Minimal ERC-8004 discovery indexer built as a custom `reth` binary. The process:

- runs a Reth execution node,
- installs an ExEx that indexes `Registered`, `URIUpdated`, and `MetadataSet`,
- persists state in SQLite with WAL mode,
- serves a small 8004scan-shaped REST API from the same container.

The binary is built as `reth` so it can replace the upstream executable in the existing
`ethereum-helm-charts/reth` chart.
