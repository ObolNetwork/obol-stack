from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

# Create core tools that are most likely to work
core_tools = [
    # 1. Filesystem MCP Server for debugging el/cl/charon nodes
    MCPToolset(
        connection_params=StdioServerParameters(
            command='npx',
            args=["-y", "@modelcontextprotocol/server-filesystem", 
                  "/Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/docs/"]
        )
    ),
    # 2. Obol MCP Server for debugging Obol clusters
    MCPToolset(
        connection_params=StdioServerParameters(
            command="uv",
            args=["run", "/Users/bussyjd/Development/Obol_Workbench/obol-mcp/server.py"],
            cwd="/Users/bussyjd/Development/Obol_Workbench/obol-mcp"
        )
    )
]

# Optional tools that might fail - add them conditionally
optional_tools = []

# Try to add Kubernetes MCP Server if available
try:
    import os
    if os.path.exists("/Users/bussyjd/Development/kubernetes-mcp-server/kubernetes-mcp-server"):
        optional_tools.append(
            MCPToolset(
                connection_params=StdioServerParameters(
                    command="/Users/bussyjd/Development/kubernetes-mcp-server/kubernetes-mcp-server",
                    args=[]
                )
            )
        )
except Exception:
    pass

# Try to add Foundry MCP Server if available
try:
    if os.path.exists("/Users/bussyjd/Development/foundry-mcp-server/dist/index.js"):
        optional_tools.append(
            MCPToolset(
                connection_params=StdioServerParameters(
                    command="node",
                    args=["/Users/bussyjd/Development/foundry-mcp-server/dist/index.js"]
                )
            )
        )
except Exception:
    pass

# Create the agent with available tools
root_agent = LlmAgent(
    model='gemini-2.0-flash',
    name='obol_agent',
    instruction=(
        'You are Obol Agent an assistant that helps users to manage their Obol clusters, '
        'Kubernetes clusters, and Foundry projects. Use the appropriate tool based on the user query. '
        'If certain tools are not available, inform the user about the limitation.'
    ),
    tools=core_tools + optional_tools
)