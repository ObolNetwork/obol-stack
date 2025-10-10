"""
Container-friendly version of the Obol Agent with AG-UI
"""
from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import McpToolset, StdioConnectionParams
from mcp.client.stdio import StdioServerParameters
from ag_ui_adk import ADKAgent, add_adk_fastapi_endpoint
from fastapi import FastAPI, Request
import uvicorn
import os
import json
from dotenv import load_dotenv

# Load environment variables
load_dotenv()

# Configuration from environment
ENABLE_MCP_SERVERS = os.getenv("ENABLE_MCP_SERVERS", "false").lower() == "true"
OBOL_MCP_PATH = os.getenv("OBOL_MCP_PATH", "/opt/obol-mcp")
K8S_MCP_PATH = os.getenv("K8S_MCP_PATH", "/opt/kubernetes-mcp-server")
FOUNDRY_MCP_PATH = os.getenv("FOUNDRY_MCP_PATH", "/opt/foundry-mcp-server")
PORT = int(os.getenv("PORT", "8000"))
MODEL = os.getenv("LLM_MODEL", "gemini-2.0-flash")

# Initialize tools list
tools = []

# Add MCP servers only if enabled and available
if ENABLE_MCP_SERVERS:
    # Obol MCP Server
    if os.path.exists(os.path.join(OBOL_MCP_PATH, "server.py")):
        try:
            tools.append(
                McpToolset(
                    connection_params=StdioConnectionParams(
                        server_params=StdioServerParameters(
                            command="python",
                            args=[os.path.join(OBOL_MCP_PATH, "server.py")],
                            cwd=OBOL_MCP_PATH
                        )
                    )
                )
            )
            print(f"✓ Loaded Obol MCP Server from {OBOL_MCP_PATH}")
        except Exception as e:
            print(f"✗ Failed to load Obol MCP Server: {e}")

    # Kubernetes MCP Server
    k8s_mcp_bin = os.path.join(K8S_MCP_PATH, "kubernetes-mcp-server")
    if os.path.exists(k8s_mcp_bin):
        try:
            tools.append(
                McpToolset(
                    connection_params=StdioConnectionParams(
                        server_params=StdioServerParameters(
                            command=k8s_mcp_bin,
                            args=[]
                        )
                    )
                )
            )
            print(f"✓ Loaded Kubernetes MCP Server from {K8S_MCP_PATH}")
        except Exception as e:
            print(f"✗ Failed to load Kubernetes MCP Server: {e}")

    # Foundry MCP Server
    foundry_mcp_js = os.path.join(FOUNDRY_MCP_PATH, "dist/index.js")
    if os.path.exists(foundry_mcp_js):
        try:
            tools.append(
                McpToolset(
                    connection_params=StdioConnectionParams(
                        server_params=StdioServerParameters(
                            command="node",
                            args=[foundry_mcp_js]
                        )
                    )
                )
            )
            print(f"✓ Loaded Foundry MCP Server from {FOUNDRY_MCP_PATH}")
        except Exception as e:
            print(f"✗ Failed to load Foundry MCP Server: {e}")

# Create the LLM agent
instruction = (
    'You are Obol Agent, an assistant that helps users manage their Obol clusters, '
    'various L1 and L2 clients running in Kubernetes clusters.'
)

if not tools:
    instruction += (
        ' Note: MCP server tools are not available in this environment. '
        'I can provide guidance and information but cannot directly interact with the cluster.'
    )
else:
    instruction += ' Use the appropriate tool based on the user query.'

obol_agent = LlmAgent(
    model=MODEL,
    name='obol_agent',
    instruction=instruction,
    tools=tools
)

# Wrap agent with AG-UI middleware
adk_agent = ADKAgent(
    adk_agent=obol_agent,
    app_name="obol_ag_ui_app",
    user_id="default_user",
    session_timeout_seconds=3600,
    use_in_memory_services=True
)

# Create FastAPI app
app = FastAPI(
    title="Obol Agent - AG UI Backend",
    description="AG-UI powered assistant for Obol Stack management",
    version="1.0.0"
)

# Add request logging middleware (optional, can be disabled via env)
if os.getenv("LOG_REQUESTS", "false").lower() == "true":
    @app.middleware("http")
    async def log_requests(request: Request, call_next):
        if request.method == "POST" and request.url.path == "/":
            body = await request.body()
            print(f"Incoming request to /: {body[:500]}")  # Log first 500 chars
        response = await call_next(request)
        return response

# Add ADK endpoint
add_adk_fastapi_endpoint(
    app=app,
    agent=adk_agent,
    path="/"
)

# Health check endpoint
@app.get("/health")
async def health_check():
    return {
        "status": "healthy",
        "agent": obol_agent.name,
        "model": MODEL,
        "tools_loaded": len(tools),
        "mcp_enabled": ENABLE_MCP_SERVERS
    }

# Info endpoint
@app.get("/info")
async def info():
    return {
        "name": "Obol Agent AG-UI",
        "version": "1.0.0",
        "model": MODEL,
        "tools": [type(tool).__name__ for tool in tools],
        "environment": "container" if os.path.exists("/.dockerenv") else "local"
    }

if __name__ == "__main__":
    print(f"Starting Obol Agent AG-UI backend on http://0.0.0.0:{PORT}")
    print(f"AG-UI endpoint available at: http://localhost:{PORT}/")
    print(f"Health check at: http://localhost:{PORT}/health")
    print(f"Info at: http://localhost:{PORT}/info")
    print(f"Model: {MODEL}")
    print(f"Tools loaded: {len(tools)}")

    uvicorn.run(app, host="0.0.0.0", port=PORT)