#!/bin/bash
set -euo pipefail
go build ./...
go test ./...  # unit tests only (no -tags integration)
