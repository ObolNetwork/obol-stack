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
        # Filesystem MCP via Docker
        MCPToolset(
            connection_params=StdioServerParameters(
                command='docker',
                args=[
                    "run", "--rm", "-i",
                    "-v", "/Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/docs/:/data",  # Adjust as needed
                    "filesystem-mcp:latest"
                ],
            ),
        ),
        # Obol MCP via Docker
        MCPToolset(
            connection_params=StdioServerParameters(
                command="docker",
                args=[
                    "run", "--rm", "-i",
                    "-v", "/Users/bussyjd/Development/Obol_Workbench/obol-mcp:/app",  # Adjust as needed
                    "obol-mcp:latest"
                ],
            ),
        ),
        # Kubernetes MCP via Docker
        MCPToolset(
            connection_params=StdioServerParameters(
                command="docker",
                args=[
                    "run", "--rm", "-i",
                    # Add any required mounts or envs for k8s-mcp
                    "kubernetes-mcp:latest"
                ],
            ),
        ),
        # # Foundry MCP via Docker
        # MCPToolset(
        #     connection_params=StdioServerParameters(
        #         command="docker",
        #         args=[
        #             "run", "--rm", "-i",
        #             # Add any required mounts or envs for foundry-mcp
        #             "foundry-mcp:latest"
        #         ],
        #     ),
        # ),
    ],
)