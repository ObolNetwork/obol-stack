# Enhanced Obol Agent for Kubernetes deployment
import os
from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

# Configuration - Environment variables for flexibility
WORKSPACE_PATH = os.getenv("OBOL_WORKSPACE_PATH", "/workspace")
DOCS_PATH = os.getenv("OBOL_DOCS_PATH", f"{WORKSPACE_PATH}/obol-adk/docs")
USE_IN_CLUSTER_CONFIG = os.getenv("USE_IN_CLUSTER_CONFIG", "false").lower() == "true"

# Agent definition for Kubernetes deployment
root_agent = LlmAgent(
    model=os.getenv("OBOL_AGENT_MODEL", "gemini-2.5-flash-preview-05-20"),
    name='obol_agent',
    instruction=(
        'You are Obol Agent running in Kubernetes, a comprehensive assistant specialized in distributed validator technology. '
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
        # Filesystem MCP - File operations and project management
        MCPToolset(
            connection_params=StdioServerParameters(
                command='docker',
                args=[
                    "run", "-i", "--rm",
                    "--mount", f"type=bind,src={WORKSPACE_PATH},dst=/projects/workspace",
                    "mcp/filesystem",
                    "/projects"
                ],
            ),
        ),
        
        # Enhanced Obol MCP - Obol API and cluster management
        MCPToolset(
            connection_params=StdioServerParameters(
                command="docker",
                args=[
                    "run", "--rm", "-i",
                    "obol-mcp:enhanced"
                ],
            ),
        ),
        
        # Kubernetes MCP - Using kubectl directly instead of docker for in-cluster access
        MCPToolset(
            connection_params=StdioServerParameters(
                command="kubectl",
                args=["exec", "-i", "kubernetes-mcp-0", "--", "kubernetes-mcp-server"]
                if USE_IN_CLUSTER_CONFIG else
                [
                    "run", "--rm", "-i",
                    "-v", f"{os.path.expanduser('~/.kube/config')}:/home/appuser/.kube/config:ro",
                    "-e", "KUBECONFIG=/home/appuser/.kube/config",
                    "mcp/kubernetes:latest"
                ],
            ),
        ) if not USE_IN_CLUSTER_CONFIG else 
        # For in-cluster, use the Node.js MCP server directly
        MCPToolset(
            connection_params=StdioServerParameters(
                command="kubernetes-mcp-server",
                args=[]
            ),
        ),
    ],
)

# Export the agent for ADK to discover
__all__ = ['root_agent']