# Obol Agents

This directory contains examples of integrating Google's Agent Development Kit (ADK) with various Model Context Protocol (MCP) servers related to Obol Stack.

> **Note:** Docker configurations have been moved to individual agent directories. See [DOCKER_NOTE.md](DOCKER_NOTE.md) for details.
## Prerequisites

1.  **Python Environment:** Ensure you have a Python 3.10+ environment set up. It's recommended to use a virtual environment:
    ```bash
    python3 -m venv .venv
    source .venv/bin/activate
    ```
2.  **Dependencies:** Install the required Python packages. Assuming the main `requirements.txt` is in the `obol-adk` root or relevant subdirectories:
    ```bash
    pip install google-adk
    ```
3.  **Environment Variables:** Create `.env` files in the respective agent directories (`obol-agent/` and `obol-agent-web/`) and populate them with necessary API keys or configurations (e.g., `GOOGLE_API_KEY`). Refer to the agent scripts for required variables. Example `.env` content:
    ```
    GOOGLE_API_KEY=your_google_api_key_here
    # Add other keys like FOUNDRY_RPC_URL if needed by specific MCP servers
    ```
4.  **MCP Servers:** Ensure the external MCP servers (Kubernetes, Foundry) are accessible and running if the agents depend on them. The Obol and Filesystem servers are typically started automatically by the ADK agent scripts.

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

This agent is designed to be run using the ADK Web UI. It connects to Filesystem, Obol, Kubernetes, and Foundry MCP servers.

**To run:**

1.  Navigate to the `obol-adk` root directory:
    ```bash
    cd /Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk
    ```
2.  Ensure your `.env` file is present in the `obol-agent-web/` subdirectory.
3.  Start the ADK Web server:
    ```bash
    adk web
    ```
4.  Open your web browser and navigate to the URL provided (usually `http://localhost:8000`).
5.  Select the `obol-agent-web` application from the dropdown list in the ADK Web UI.
6.  Interact with the agent through the chat interface.


### 3. AG-UI Backend Agent (`obol-agent-ag-ui/agent.py`)

This agent provides an AG-UI backend for interacting with Obol Stack through a modern web interface.

**To run locally:**

1.  Navigate to the `obol-agent-ag-ui` directory:
    ```bash
    cd /Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/obol-agent-ag-ui
    ```
2.  Install dependencies:
    ```bash
    pip install -r requirements.txt
    ```
3.  Run the agent:
    ```bash
    python agent.py
    ```
    The AG-UI backend will be available at `http://localhost:8000/`

**To run with Docker:**

1.  Navigate to the `obol-agent-ag-ui` directory
2.  Build and run with Docker Compose:
    ```bash
    docker-compose up -d
    ```
    Or build manually:
    ```bash
    docker build -t obol-agent-ag-ui:latest .
    docker run -d -p 8000:8000 obol-agent-ag-ui:latest
    ```

See the [obol-agent-ag-ui README](obol-agent-ag-ui/README.md) for detailed configuration options.

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
