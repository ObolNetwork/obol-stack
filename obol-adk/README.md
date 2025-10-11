# Obol Agents

This directory contains examples of integrating Google's Agent Development Kit (ADK) with various Model Context Protocol (MCP) servers related to Obol Stack.

> **Note:** Docker configurations have been moved to individual agent directories. See [DOCKER_NOTE.md](DOCKER_NOTE.md) for details.

## Quick Start

For the AG-UI agent (recommended), use the Makefile workflow:

```bash
# One command to set up everything and start the agent
make dev

# Or run individual steps:
make install  # Create venv and install dependencies
make setup    # Clone obol-gitbook and configure .env
make start    # Start the agent
make test     # Run tests
```

See [Development Workflow](#development-workflow) below for details.

## Prerequisites

1.  **Python Environment:** Python 3.12+ is required for the AG-UI agent.
2.  **Google API Key:** Required for the Gemini model. Get your key from [Google AI Studio](https://aistudio.google.com/app/apikey).
3.  **Environment Variables:** Create a `.env` file in the `obol-adk` directory:
    ```bash
    GOOGLE_API_KEY=your_google_api_key_here
    FILESYSTEM_MCP_PATHS=/path/to/obol-gitbook  # Auto-configured by 'make setup'
    ```
4.  **MCP Servers:** The agents use published MCP packages via `uvx` and `npx`:
    - `obol-mcp` - Obol cluster operations
    - `@modelcontextprotocol/server-filesystem` - Documentation access
    - `mcp-server-kubernetes` - Kubernetes operations (optional)

## Running the Agents

### 1. Command-Line Agent (`obol-agent/agent.py`)

This agent connects to Obol, Kubernetes, and Foundry MCP servers and runs directly in the terminal.

**To run:**

1.  Navigate to the `obol-agent` directory:
    ```bash
    cd /Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/obol-agent
    ```
2.  Ensure your `.env` file is present in this directory.
3.  Run the agent script:
    ```bash
    python3 agent.py
    ```
    The script will prompt for a user query after connecting to the servers.

### 2. Web UI Agent (`obol-agent-web/agent.py`)

This agent is designed to be run using the ADK Web UI. It connects to Obol, Kubernetes, and Filesystem MCP servers.

**To run (recommended):**

```bash
# From the obol-adk directory
make start-web
```

This will launch the ADK Web interface. Make sure to select `obol_agent` from the dropdown in the web UI.

**Manual alternative:**

1.  Navigate to the `obol-adk` root directory:
    ```bash
    cd /Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk
    ```
2.  Ensure your `.env` file is configured (see `.env.example`)
3.  Start the ADK Web server:
    ```bash
    cd obol-agent-web && adk web
    ```
4.  Open your web browser and navigate to the URL provided
5.  Select `obol_agent` from the dropdown list in the ADK Web UI
6.  Interact with the agent through the chat interface


### 3. AG-UI Backend Agent (`obol-agent-ag-ui/agent.py`) - Recommended

This agent provides an AG-UI backend for interacting with Obol Stack through a modern web interface. It includes access to Obol documentation via the filesystem MCP server.

**To run locally (recommended):**

Use the Makefile workflow from the `obol-adk` directory:

```bash
# First time setup - creates venv, installs deps, clones docs, configures .env
make dev

# Subsequent runs
make start
```

The AG-UI backend will be available at `http://localhost:8000/`

**Manual setup (alternative):**

1.  Navigate to the `obol-adk` directory:
    ```bash
    cd /Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk
    ```
2.  Create and activate virtual environment:
    ```bash
    python3 -m venv .venv
    source .venv/bin/activate  # On Windows: .venv\Scripts\activate
    ```
3.  Install dependencies:
    ```bash
    pip install -r requirements.txt
    ```
4.  Clone documentation:
    ```bash
    git clone https://github.com/ObolNetwork/obol-gitbook.git
    ```
5.  Create `.env` file with required variables (see `.env.example`)
6.  Run the agent:
    ```bash
    cd obol-agent-ag-ui && python agent.py
    ```

**To run with Docker:**

1.  Navigate to the `obol-agent-ag-ui` directory
2.  Build and run with Docker Compose:
    ```bash
    docker-compose up -d
    ```
    Or build manually:
    ```bash
    docker build -t obol-agent-ag-ui:latest .
    docker run -d -p 8000:8000 --env-file ../.env obol-agent-ag-ui:latest
    ```

See the [obol-agent-ag-ui README](obol-agent-ag-ui/README.md) for detailed configuration options.

## Development Workflow

The `obol-adk` directory includes a Makefile for convenient development workflows:

### Available Commands

```bash
make help       # Show all available commands
make dev        # Full setup and start (one command for first-time setup)
make install    # Create virtual environment and install dependencies
make setup      # Clone/update obol-gitbook and configure .env
make start      # Start the obol-agent-ag-ui agent (FastAPI backend)
make start-web  # Start the obol-agent-web agent (ADK Web UI)
make test       # Run agent unit tests
make ci         # Run full CI workflow locally (simulates GitHub Actions)
make clean      # Remove obol-gitbook directory
```

### How It Works

1. **`make install`** - Sets up Python environment
   - Creates `.venv/` virtual environment (if it doesn't exist)
   - Installs all dependencies from `requirements.txt`
   - Upgrades pip to latest version

2. **`make setup`** - Configures project-specific settings
   - Clones [obol-gitbook](https://github.com/ObolNetwork/obol-gitbook) to `./obol-gitbook/`
   - Updates `FILESYSTEM_MCP_PATHS` in `.env` to point to the docs
   - Preserves your existing `GOOGLE_API_KEY`

3. **`make start`** - Runs the agent
   - Verifies virtual environment exists
   - Starts the AG-UI backend on `http://localhost:8000`

4. **`make dev`** - Combines `install` + `setup` + `start`
   - Perfect for first-time setup
   - One command to go from zero to running agent

5. **`make test`** - Runs unit tests
   - Quick test suite for development
   - Tests agent health and basic functionality

6. **`make ci`** - Full CI workflow simulation
   - Simulates the exact GitHub Actions workflow
   - Runs agent in background, performs health checks, runs full test suite
   - Perfect for validating changes before pushing
   - Requires `GOOGLE_API_KEY` in `.env`

### First Time Setup

```bash
# 1. Clone the repository (if you haven't already)
git clone https://github.com/ObolNetwork/obol-stack.git
cd obol-stack/obol-adk

# 2. Create .env file with your API key
cp .env.example .env
# Edit .env and add your GOOGLE_API_KEY

# 3. Run dev workflow
make dev
```

The agent will be available at `http://localhost:8000/` with access to:
- Obol cluster operations via `obol-mcp`
- Obol documentation via `@modelcontextprotocol/server-filesystem`
- Kubernetes operations via `mcp-server-kubernetes` (if available)

## Improvements

- Awaiting MCP public package for Foundry MCP server https://github.com/PraneshASP/foundry-mcp-server?tab=readme-ov-file#setup-using-npm-package

## Docker Images for MCP Servers

Dockerfiles that package the MCP servers used by `obol-agent-web` are located in the `dockerfiles/` directory:

- **Dockerfile.filesystem** – packages the Filesystem MCP server.
- **Dockerfile.obol** – packages the Obol MCP server.
- **Dockerfile.kubernetes** – packages the Kubernetes MCP server.
- **Dockerfile.foundry** – packages the Foundry MCP server.

These images can be built and pushed to your own registry, for example:

```bash
docker build -f dockerfiles/Dockerfile.obol -t <registry>/obol-mcp:latest .
```
