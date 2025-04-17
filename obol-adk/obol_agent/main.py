import asyncio
import logging
from dotenv import load_dotenv

from google.adk.runners import Runner, SessionService
from google.adk.web import app
from google.genai import types
from obol_monitoring_agent.agents.agent import add_kubernetes_tools

# Configure logging
logging.basicConfig(level=logging.INFO, format='%(asctime)s - %(levelname)s - %(message)s')

# Load environment variables from .env file at the project root
load_dotenv(dotenv_path='../.env')

APP_NAME = "obol_monitoring_agent" # Should match directory name
USER_ID = "cluster_admin"

async def run_agent_interaction(query: str):
    """Runs a single interaction with the Obol agent system."""
    session_service = SessionService()
    session_id = f"session_{query[:10].replace(' ', '_')}" # Simple session ID based on query

    # --- Get Initialized Agent and Exit Stack ---
    # This now handles tool initialization internally
    from obol_monitoring_agent.agents import obol_agent

    if not obol_agent:
        logging.error("Failed to initialize the root agent. Exiting.")
        return

    # Create session *after* agent might be fully configured
    session = session_service.create_session(
        app_name=APP_NAME, user_id=USER_ID, session_id=session_id
    )

    runner = Runner(
        agent=obol_agent,
        app_name=APP_NAME,
        session_service=session_service,
        # Add artifact/memory service if needed by other agents/tools
    )

    logging.info(f"\n--- Sending Query to Obol Agent: '{query}' ---")
    content = types.Content(role='user', parts=[types.Part(text=query)])

    final_response_text = "Agent did not produce a final response."
    try:
        async for event in runner.run_async(
            user_id=USER_ID, session_id=session_id, new_message=content
        ):
            logging.info(f"Event: Author={event.author}, Final={event.is_final_response()}, Content Snippet={str(event.content)[:150]}...")
            # Simple extraction for demo
            if event.is_final_response() and event.content and event.content.parts:
                 response_part = event.content.parts[0]
                 if response_part.text:
                     final_response_text = response_part.text.strip()
                 elif response_part.function_response: # If last event is tool result itself
                     final_response_text = f"[Tool Result: {response_part.function_response.name} -> {str(response_part.function_response.response)[:100]}...]"
                 elif response_part.function_call: # Should ideally not be final, but handle
                      final_response_text = f"[Agent requested tool: {response_part.function_call.name}]"

    except Exception as e:
        logging.error(f"Error during agent run: {e}", exc_info=True)
        final_response_text = f"An error occurred: {e}"
    finally:
        # --- Crucial Cleanup ---
        # Removed mcp_exit_stack as it's not imported


    print(f"\n--- Final Response for Query '{query}' ---")
    print(final_response_text)
    print("-" * 50)


if __name__ == "__main__":
    asyncio.run(add_kubernetes_tools())
    from obol_monitoring_agent.agents import obol_agent
    app.configure_agent(obol_agent)
    try:
        asyncio.run(app.run())
    except RuntimeError as e:
        if "cannot be called from a running event loop" in str(e):
            logging.warning("Running in an existing event loop (e.g., Jupyter). Use 'await main()' instead.")
        else:
            raise e
