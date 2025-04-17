import asyncio
import logging
from google.adk.agents import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, SseServerParams, StdioServerParameters
from obol_monitoring_agent import evm_tools, dvt_tools

# --- Model Configuration ---
ORCHESTRATOR_MODEL = "gemini-2.0-flash" # Adjust as needed
SPECIALIST_MODEL = "gemini-2.0-flash"   # Can be different for sub-agents

# --- Specialized Agents ---

evm_agent = LlmAgent(
    name="evm_agent",
    model=SPECIALIST_MODEL,
    instruction="You are an expert on EVM blockchains. Use the provided tools to answer questions about addresses, balances, and contracts.",
    description="Handles tasks related to EVM chains, like checking balances or contract info.",
    tools=[
        evm_tools.get_eth_balance,
        evm_tools.get_contract_info,
        # Add more evm_tools here
    ]
)

dvt_agent = LlmAgent(
    name="dvt_agent",
    model=SPECIALIST_MODEL,
    instruction="You are an expert on Obol Distributed Validator Technology. Use your tools to provide information about DVT cluster status and validator performance.",
    description="Handles tasks related to Obol DVT, like checking cluster status or validator performance.",
    tools=[
        dvt_tools.get_dvt_cluster_status,
        dvt_tools.get_validator_performance,
        # Add more dvt_tools here
    ]
)

# --- Root Orchestrator Agent ---
obol_agent = LlmAgent(
    name="obol_agent",
    model=ORCHESTRATOR_MODEL,
    instruction="""You are the Obol central coordinator. Analyze the user's request and delegate it to the appropriate specialized agent:
- If the user asks to list the available tools, respond with a list of the available tools.
- For EVM tasks (balance checks, contracts), delegate to 'evm_agent'.
- For DVT tasks (cluster status, validator performance), delegate to 'dvt_agent'.
If unsure, clarify with the user.
If the user is asking a question that is not related to EVM or DVT, respond politely that you can only answer questions about EVM or DVT.""",
    description="Main coordinator for Obol-related tasks, delegating to specialized agents.",
    sub_agents=[evm_agent, dvt_agent] # Link sub-agents
)

obol_agent.tools = []

async def get_kubernetes_mcp_tools():
    """Gets tools from the Kubernetes MCP Server."""
    print("Attempting to connect to Kubernetes MCP server...")
    tools, exit_stack = await MCPToolset.from_server(
        connection_params=StdioServerParameters(
            command='npx',
            args=["-y", "kubernetes-mcp-server@latest"]
        )
    )
    print("MCP Toolset created successfully.")
    return tools, exit_stack

async def add_kubernetes_tools():
    tools, exit_stack = await get_kubernetes_mcp_tools()
    obol_agent.tools.extend(tools)
