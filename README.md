<div align="center">
  <img src="https://obol.org/obolnetwork.png" alt="Obol banner" />

&nbsp;

  <h1>Obol Stack Management CLI</h1>

</div>

## Overview

The Obol Stack is a CLI tool for managing local Kubernetes clusters and Ethereum node infrastructure. It provides a simple interface for cluster lifecycle management with XDG-compliant directory structure.

## Getting Started

> [!IMPORTANT]
> The Obol Stack is alpha software. It is not complete, and it may not be working smoothly. If you encounter an issue that does not appear to be documented, please open a [github issue](http://github.com/obolNetwork/obol-stack/issues) if an appropriate one is not already present.

### Prerequisites

- [Docker](https://www.docker.com/) engine installed
- [Go](https://golang.org/) 1.25+ (for building from source)

### Installation

Use the `obolup.sh` bootstrap installer to build and install the `obol` CLI:

```sh
# Clone this repo
git clone git@github.com:ObolNetwork/obol-stack.git
cd obol-stack

# Run the installer
./obolup.sh
```

The installer will:
- Validate Docker is installed
- Build the `obol` binary from source
- Install it to the appropriate bin directory

The installer is idempotent - running it multiple times will upgrade existing installations.

### Basic Usage

```sh
# Initialize a new cluster configuration
obol cluster init

# Start the cluster
obol cluster up

# Stop the cluster
obol cluster down

# Connect to cluster services
obol cluster connect

# Backup cluster data
obol cluster backup

# Purge all cluster data
obol cluster purge
```

## Configuration

The CLI follows the [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html):

- **Config directory**: `$XDG_CONFIG_HOME/obol` (default: `~/.config/obol`)
- **Bin directory**: `$XDG_CONFIG_HOME/obol/bin` (default: `~/.config/obol/bin`)
- **State directory**: `$XDG_DATA_HOME/obol` (default: `~/.local/share/obol`)

### Environment Variable Overrides

You can override default paths with environment variables:

```sh
export OBOL_CONFIG_DIR=/custom/config/path
export OBOL_BIN_DIR=/custom/bin/path
export OBOL_STATE_DIR=/custom/state/path
```

Or use CLI flags:

```sh
obol --config-dir=/custom/path cluster init
```

Priority: CLI flags > Environment variables > XDG defaults

### Development Mode

For local development, use the `OBOL_DEVELOPMENT` flag to use a local `.workspace` directory instead of system paths:

```sh
export OBOL_DEVELOPMENT=true
./obolup.sh
```

This creates an isolated development environment in `.workspace/` with the same structure as production:

```
.workspace/
├── bin/           # Binary location
├── config/        # Configuration files
└── share/         # State and data
```

For persistent local development settings, copy `.envrc.local.example` to `.envrc.local` and customize as needed. This file is gitignored and automatically loaded by [direnv](https://direnv.net/).

## Project Status

This project is currently in early alpha development. The CLI structure and core commands are in place, but implementations are still being developed.

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
