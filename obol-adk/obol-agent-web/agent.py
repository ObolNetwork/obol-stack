# Enhanced Obol Agent for ADK Web with comprehensive MCP toolsets
import os
from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool import StdioConnectionParams
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset
from mcp import StdioServerParameters

# Configuration - Environment variables for flexibility
WORKSPACE_PATH = os.getenv("OBOL_WORKSPACE_PATH", "/Users/bussyjd/Development/Obol_Workbench/obol-stack")
DOCS_PATH = os.getenv("OBOL_DOCS_PATH", f"{WORKSPACE_PATH}/obol-adk/docs")
KUBECONFIG_PATH = os.getenv("KUBECONFIG", os.path.expanduser("~/.kube/config"))

# Agent definition for ADK web with comprehensive MCP toolsets
root_agent = LlmAgent(
    model=os.getenv("OBOL_AGENT_MODEL", "gemini-2.5-flash-preview-05-20"),
    name='obol_agent',
    instruction=(
        'You are Obol Agent, a comprehensive assistant specialized in distributed validator technology. '
        'You help users manage:\n'
        '• Obol Distributed Validator clusters and networks\n'
        '• Kubernetes deployments and container orchestration\n'
        '• Foundry smart contract development and testing\n'
        '• File system operations and project management\n\n'
        'Use the most appropriate tool(s) for each user query. You have access to:\n'
        '- Obol API for cluster management and network status\n'
        '- Kubernetes tools for container orchestration\n'
        '- Foundry tools for smart contract development\n'
        '- File system tools for project management\n\n'
        'Always provide clear, actionable responses and suggest relevant follow-up actions.'
    ),
    tools=[
        # Filesystem MCP - File operations and project management (Official MCP Community)
        MCPToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command='docker',
                    args=[
                        "run", "-i", "--rm",
                        "--mount", f"type=bind,src={WORKSPACE_PATH},dst=/projects/workspace",
                        "mcp/filesystem",
                        "/projects"
                    ],
                ),
                timeout=10
            )
        ),
        
        # Enhanced Obol MCP - Obol API and cluster management
        MCPToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="docker",
                    args=[
                        "run", "--rm", "-i",
                    "obol-mcp:latest"
                    ],
                ),
                timeout=10
            )
        ),
        
        # Kubernetes MCP - Container orchestration and cluster management
        # Current: https://github.com/manusa/kubernetes-mcp-server
        # Alternatives: https://github.com/Flux159/mcp-server-kubernetes
        MCPToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="docker",
                    args=[
                        "run", "--rm", "-i",
                        "--network", "host",
                        "-v", f"{KUBECONFIG_PATH}:/home/appuser/.kube/config",
                        "-e", "K8S_NAMESPACE=l1",
                        "flux159/mcp-server-kubernetes:latest"
                    ],
                ),
                timeout=60,
                # Optional: Filter which tools from the MCP server are exposed
                tool_filter=['kubectl_delete']
            )
        ),
        
        # Foundry MCP - Smart contract development and testing
        MCPToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="docker",
                    args=[
                        "run", "--rm", "-i",
                        "--network", "host",
                        "foundry-mcp-server:latest"
                    ],
                ),
                timeout=20
            ),
        ),
    ],
)

# Export the agent for ADK to discover
__all__ = ['root_agent']