# ./adk_agent_samples/mcp_agent/agent.py
from google.adk.agents.llm_agent import LlmAgent
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, StdioServerParameters
from contextlib import AsyncExitStack # <--- Import AsyncExitStack
import os # For environment variable access if needed for Foundry


async def create_agent():
    """Creates an ADK Agent equipped with tools from multiple MCP Servers."""
    all_tools = []
    # Create a single exit stack to manage all MCP connections
    main_exit_stack = AsyncExitStack()

    async with main_exit_stack:
        # 1. Filesystem Server
        # Useful to debug el/cl/charon nodes
        print("Connecting to Filesystem MCP server...")
        fs_tools, fs_exit_stack = await MCPToolset.from_server(
            connection_params=StdioServerParameters(
                command='npx',
                args=["-y",
                      "@modelcontextprotocol/server-filesystem",
                      # Ensure this path exists and is absolute
                      "/Users/bussyjd/Development/Obol_Workbench/obol-stack/obol-adk/docs/",
                      ],
            )
        )
        all_tools.extend(fs_tools)
        await main_exit_stack.enter_async_context(fs_exit_stack)
        print(f"Fetched {len(fs_tools)} tools from Filesystem MCP server.")

        # 2. Obol Server
        # Useful to debug Obol cluster
        print("Connecting to Obol MCP server...")
        obol_tools, obol_exit_stack = await MCPToolset.from_server(
            connection_params=StdioServerParameters(
                # Using uv runner as specified
                command="uv",
                args=[
                    "run",
                    # Specify the full path to server.py for clarity
                    "/Users/bussyjd/Development/Obol_Workbench/obol-mcp/server.py"
                ],
                # Specify the working directory for the uv command
                cwd="/Users/bussyjd/Development/Obol_Workbench/obol-mcp"
            )
        )
        all_tools.extend(obol_tools)
        await main_exit_stack.enter_async_context(obol_exit_stack)
        print(f"Fetched {len(obol_tools)} tools from Obol MCP server.")

        # 3. Kubernetes Server (Uncommented)
        # Useful to debug Kubernetes cluster
        print("Connecting to Kubernetes MCP server...")
        k8s_tools, k8s_exit_stack = await MCPToolset.from_server(
            connection_params=StdioServerParameters(
                command="npx",
                args=[
                    ## https://github.com/manusa/kubernetes-mcp-server/releases/tag/v0.0.31
                    #command="/Users/bussyjd/Development/kubernetes-mcp-server/kubernetes-mcp-server",
                    #args=[]
                    # Use the latest version of the Kubernetes MCP server for now
                    "mcp-server-kubernetes@latest"
                ]
            )
        )
        all_tools.extend(k8s_tools)
        await main_exit_stack.enter_async_context(k8s_exit_stack)
        print(f"Fetched {len(k8s_tools)} tools from Kubernetes MCP server.")

        # 4. Foundry Server
        # Useful to debug Foundry projects
        print("Connecting to Foundry MCP server...")
        foundry_tools, foundry_exit_stack = await MCPToolset.from_server(
            connection_params=StdioServerParameters(
                command="node",
                args=["/Users/bussyjd/Development/foundry-mcp-server/dist/index.js"],
                # Pass env vars if needed, e.g.:
                # env={"RPC_URL": os.getenv("FOUNDRY_RPC_URL")} 
            )
        )
        all_tools.extend(foundry_tools)
        await main_exit_stack.enter_async_context(foundry_exit_stack)
        print(f"Fetched {len(foundry_tools)} tools from Foundry MCP server.")

        print(f"Total tools fetched: {len(all_tools)}.")

        # Create the agent with all collected tools
        agent = LlmAgent(
            model='gemini-2.0-flash', 
            name='obol_agent',
            instruction=(
                'You are Obol Agent an assistant that helps users to mange their Obol clusters, ' 
                'Kubernetes clusters, and Foundry projects. Use the appropriate tool based on the user query.'
            ),
            tools=all_tools,
        )
        
        # IMPORTANT: Pop the stack BEFORE exiting the 'async with' block
        # This transfers ownership of the cleanup tasks to the caller (adk web)
        stack_to_return = main_exit_stack.pop_all()
        print("Agent created, returning agent and exit stack.")
        return agent, stack_to_return

# This assignment is needed for adk web to find the agent
root_agent = create_agent()