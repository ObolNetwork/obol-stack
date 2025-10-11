from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import McpToolset, StdioConnectionParams
from mcp.client.stdio import StdioServerParameters
import os
from dotenv import load_dotenv

# Load environment variables from parent directory .env file
env_path = os.path.join(os.path.dirname(__file__), '..', '.env')
load_dotenv(env_path)

# Create core tools
core_tools = [
    # Obol MCP Server for debugging Obol clusters
    McpToolset(
        connection_params=StdioConnectionParams(
            server_params=StdioServerParameters(
                command="uvx",
                args=["obol-mcp"]
            )
        )
    )
]

# Add filesystem MCP servers for each configured path
filesystem_paths = os.getenv('FILESYSTEM_MCP_PATHS', '')
if filesystem_paths:
    for path in filesystem_paths.split(','):
        path = path.strip()
        if path and os.path.exists(path):
            core_tools.append(
                McpToolset(
                    connection_params=StdioConnectionParams(
                        server_params=StdioServerParameters(
                            command='npx',
                            args=["-y", "@modelcontextprotocol/server-filesystem", path]
                        )
                    )
                )
            )

# Optional tools
optional_tools = []

# Try to add Kubernetes MCP Server if available
try:
    optional_tools.append(
        McpToolset(
            connection_params=StdioConnectionParams(
                server_params=StdioServerParameters(
                    command="uvx",
                    args=["mcp-server-kubernetes"]
                )
            )
        )
    )
except Exception:
    pass

# Create the LLM agent
root_agent = LlmAgent(
    model='gemini-2.0-flash',
    name='obol_agent',
    instruction=(
        'You are Obol Agent, an assistant that helps users manage their Obol clusters, various L1 and L2 clients '
        'running in Kubernetes clusters. '
        '\n\n'
        'CRITICAL TOOL USAGE FOR DOCUMENTATION QUESTIONS:\n'
        'When asked about Obol concepts, SDK usage, cluster setup, configuration, or best practices, you MUST:\n'
        '1. First call list_allowed_directories to see what documentation is available\n'
        '2. Then use search_files with relevant keywords (e.g., "SDK", "quickstart", "DV", "cluster") OR use list_directory to explore\n'
        '3. Read the relevant .md files using read_file\n'
        '4. Provide the answer based on what you read\n'
        '\n'
        'NEVER ask the user for directory paths or say you lack information - you have list_allowed_directories and search_files tools. '
        'Use them automatically without asking for permission.\n'
        '\n'
        'When providing answers from documentation:\n'
        '- DO NOT mention that you are searching or reading files\n'
        '- Just provide the information naturally as if you know it\n'
        '- Be comprehensive and helpful\n'
        '\n'
        'For Kubernetes queries, use kubectl tools. For Obol cluster operations, use obol tools. '
        'Use the appropriate tool based on the user query.'
    ),
    tools=core_tools + optional_tools
)
