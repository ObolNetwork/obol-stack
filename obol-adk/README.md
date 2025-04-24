# Obol ADK Agents

This directory contains examples of integrating Google's Agent Development Kit (ADK) with various Model Context Protocol (MCP) servers related to Obol, Kubernetes, Foundry, and local filesystems.

## Prerequisites

1.  **Python Environment:** Ensure you have a Python 3.10+ environment set up. It's recommended to use a virtual environment:
    ```bash
    python3 -m venv .venv
    source .venv/bin/activate
    ```
2.  **Dependencies:** Install the required Python packages. Assuming the main `requirements.txt` is in the `obol-adk` root or relevant subdirectories:
    ```bash
    pip install -r requirements.txt
    # Or install specific requirements if separated, e.g.:
    # pip install -r obol-agent/requirements.txt
    # pip install -r obol-agent-web/requirements.txt
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
