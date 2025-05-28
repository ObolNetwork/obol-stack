# ./adk_agent_samples/mcp_agent/agent.py
import os
from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters

# Agent definition for ADK web with MCPToolset tools (official pattern)
root_agent = LlmAgent(
    model='gemini-2.0-flash',
    name='obol_agent',
    instruction=(
        'You are Obol Agent, an assistant that helps users to manage their Obol clusters, '
        'Kubernetes clusters, and Foundry projects. Use the appropriate tool based on the user query.'
    ),
    tools=[
        MCPToolset(
            connection_params=StdioServerParameters(
                command='npx',
                args=[
                    "-y",
                    "@modelcontextprotocol/server-filesystem",
                    "/Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/docs/",
                ],
            ),
        ),
        MCPToolset(
            connection_params=StdioServerParameters(
                command="uv",
                args=[
                    "run",
                    "/Users/bussyjd/Development/Obol_Workbench/obol-mcp/server.py",
                ],
                cwd="/Users/bussyjd/Development/Obol_Workbench/obol-mcp",
            ),
        ),
        MCPToolset(
            connection_params=StdioServerParameters(
                command="npx",
                args=[
                    "mcp-server-kubernetes@latest",
                ],
            ),
        ),
        MCPToolset(
            connection_params=StdioServerParameters(
                command="node",
                args=["/Users/bussyjd/Development/foundry-mcp-server/dist/index.js"],
            ),
        ),
    ],
)