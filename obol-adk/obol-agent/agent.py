import asyncio
from dotenv import load_dotenv
from google.genai import types
from google.adk.agents.llm_agent import LlmAgent
from google.adk.runners import Runner
from google.adk.sessions import InMemorySessionService
from google.adk.artifacts.in_memory_artifact_service import InMemoryArtifactService # Optional
from google.adk.tools.mcp_tool.mcp_toolset import MCPToolset, SseServerParams, StdioServerParameters
from contextlib import AsyncExitStack # Import AsyncExitStack  managing the cleanup of multiple resources

# Use absolute path for certainty
dotenv_path = './.env'
load_dotenv(dotenv_path=dotenv_path)
print(f"Attempting to load .env from: {dotenv_path}")

# --- Step 1a: Import Obol Tools from MCP Server ---
async def get_obol_tools_async(exit_stack: AsyncExitStack):
  """Gets tools from the Obol MCP Server."""
  print("Attempting to connect to Obol MCP server...")
  tools, mcp_exit_stack = await MCPToolset.from_server(
      # Use StdioServerParameters for local process communication
      connection_params=StdioServerParameters(
          command="uv",
          args=[
              "--directory",
              "/Users/bussyjd/Development/Obol_Workbench/obol-mcp",
              "run",
              "server.py"
          ]
      )
  )
  print("Obol MCP Toolset created successfully.")
  # Push the MCP toolset's cleanup context onto the shared exit stack
  await exit_stack.enter_async_context(mcp_exit_stack)
  return tools

# --- Step 1b: Import Kubernetes Tools from MCP Server ---
async def get_kubernetes_tools_async(exit_stack: AsyncExitStack):
  """Gets tools from the Kubernetes MCP Server."""
  print("Attempting to connect to Kubernetes MCP server...")
  tools, mcp_exit_stack = await MCPToolset.from_server(
      connection_params=StdioServerParameters(
          command="/Users/bussyjd/Development/kubernetes-mcp-server/kubernetes-mcp-server",
          args=[]
      )
  )
  print("Kubernetes MCP Toolset created successfully.")
  # Push the MCP toolset's cleanup context onto the shared exit stack
  await exit_stack.enter_async_context(mcp_exit_stack)
  return tools

# --- Step 1c: Import Foundry Tools from MCP Server ---
async def get_foundry_tools_async(exit_stack: AsyncExitStack):
  """Gets tools from the Foundry MCP Server."""
  print("Attempting to connect to Foundry MCP server...")
  # Note: Foundry server might require environment variables like RPC_URL
  # Pass them via StdioServerParameters' 'env' argument if needed.
  tools, mcp_exit_stack = await MCPToolset.from_server(
      connection_params=StdioServerParameters(
          command="node",
          args=["/Users/bussyjd/Development/foundry-mcp-server/dist/index.js"],
          # Example: env={"RPC_URL": os.getenv("RPC_URL")}
      )
  )
  print("Foundry MCP Toolset created successfully.")
  # Push the MCP toolset's cleanup context onto the shared exit stack
  await exit_stack.enter_async_context(mcp_exit_stack)
  return tools

# --- Step 2: Obol Agent Definition ---
async def get_agent_async():
  """Creates an ADK Agent equipped with tools from the MCP Server."""
  all_tools = []
  async with AsyncExitStack() as exit_stack:
      # Get Obol Tools
      obol_tools = await get_obol_tools_async(exit_stack)
      all_tools.extend(obol_tools)
      print(f"Fetched {len(obol_tools)} tools from Obol MCP server.")

      # Get Kubernetes Tools
      kubernetes_tools = await get_kubernetes_tools_async(exit_stack)
      all_tools.extend(kubernetes_tools)
      print(f"Fetched {len(kubernetes_tools)} tools from Kubernetes MCP server.")

      # Get Foundry Tools
      foundry_tools = await get_foundry_tools_async(exit_stack)
      all_tools.extend(foundry_tools)
      print(f"Fetched {len(foundry_tools)} tools from Foundry MCP server.")

      print(f"Total tools fetched: {len(all_tools)} from 3 servers.")

      # Create Agent with combined tools
      root_agent = LlmAgent(
          name='obol_agent', # Consider renaming if it now handles K8s too
          model='gemini-1.5-flash', # Adjust model name if needed
          instruction=(
              'You are an assistant specialized in interacting with Obol Network API, Kubernetes clusters, and Foundry projects. ' 
              'Use the available Obol tools to answer questions about Obol cluster locks, ' 
              'cluster effectiveness, validator information, and network details. ' 
              'Use the available Kubernetes tools to interact with Kubernetes resources (pods, deployments, services, helm charts etc.). ' 
              'Use the available Foundry tools to interact with smart contracts, run tests, simulations, or perform other Foundry tasks. ' 
              'Pay close attention to hashes (like lock_hash or config_hash) provided by the user ' 
              'and use the appropriate Obol tool based on the query. ' 
              'For Kubernetes queries, identify the resource type and namespace if provided. ' 
              'For Foundry queries, identify the contract, function, or test target.'
          ),
          tools=all_tools, # Provide the combined MCP tools
      )
      print("Agent created successfully.")

      # --- Important: Manage ExitStack Lifetime ---
      # The exit_stack manages the cleanup of MCP connections.
      # We need to transfer its management to the caller (async_main)
      # so connections stay open while the agent runs.
      # We do this by *popping* all contexts before exiting the 'async with'.
      # The caller (async_main) will then adopt this stack.
      # See: https://docs.python.org/3/library/contextlib.html#contextlib.AsyncExitStack.pop_all
      exit_stack_to_return = exit_stack.pop_all()
      return root_agent, exit_stack_to_return

# --- Step 3: Main Execution Logic ---
async def async_main():
  """Initializes and runs the ADK Agent."""

  # Get the agent and the ExitStack manager
  root_agent, exit_stack = await get_agent_async()

  # Adopt the ExitStack to manage cleanup for MCP connections
  async with exit_stack:
      session_service = InMemorySessionService()
      artifact_service = InMemoryArtifactService()

      session = session_service.create_session(
          state={}, app_name='mcp_obol_app', user_id='user_obol'
      )

      # Example Query - Modify as needed to test Obol or K8s tools
      # query = "What's the effectiveness of this cluster? 0x168624fee311d155a9ed4fbcae8369526c91145858c0ed7084ead50eeba73db7"
    #   query = "List the helm charts in my kubernetes clusters"
      query = "get the ethereum balance of 0x85C6dA74D4ca44BA350C0e4c455bEc3EB8Ed2c5d"
      print(f"User Query: '{query}'")
      content = types.Content(role='user', parts=[types.Part(text=query)])

      runner = Runner(
          app_name='mcp_obol_app',
          agent=root_agent,
          artifact_service=artifact_service, # Optional
          session_service=session_service,
      )

      print("Running agent...")
      events_async = runner.run_async(
          session_id=session.id, user_id=session.user_id, new_message=content
      )

      async for event in events_async:
        print(f"Event received: {event}")

      # Note: Cleanup is handled by 'async with exit_stack:' context manager
      print("Agent run complete. Cleanup handled by ExitStack.")

if __name__ == '__main__':
  try:
    asyncio.run(async_main())
  except Exception as e:
      print(f"An error occurred: {e}")