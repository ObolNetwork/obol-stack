import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from '@copilotkit/runtime';
import { HttpAgent } from '@ag-ui/client';
import { NextRequest } from 'next/server';

// Using ExperimentalEmptyAdapter because the ADK agent handles LLM calls internally
const serviceAdapter = new ExperimentalEmptyAdapter();

// Get agent URL from environment with fallback to local development server
const agentUrl = process.env.ADK_AGENT_URL || 'http://127.0.0.1:8000/';

const runtime = new CopilotRuntime({
  agents: {
    obol_agent: new HttpAgent({
      url: agentUrl,
    }),
  },
});

export const POST = async (req: NextRequest) => {
  console.log('[CopilotKit Route] Incoming request to /api/copilotkit');
  console.log(
    '[CopilotKit Route] Agent URL:',
    process.env.ADK_AGENT_URL || 'http://127.0.0.1:8000/'
  );

  const { handleRequest } = copilotRuntimeNextJSAppRouterEndpoint({
    runtime,
    serviceAdapter,
    endpoint: '/api/copilotkit',
  });

  try {
    const response = await handleRequest(req);
    return response;
  } catch (error) {
    console.error('[CopilotKit Route] Error:', error);

    const errorMessage =
      error instanceof Error ? error.message : 'Unknown error occurred';

    // Check if it's a connection error to the agent
    const isConnectionError =
      errorMessage.includes('ECONNREFUSED') ||
      errorMessage.includes('ENOTFOUND') ||
      errorMessage.includes('fetch failed') ||
      errorMessage.includes('network');

    if (isConnectionError) {
      console.error(
        '[CopilotKit Route] Agent service unavailable at:',
        agentUrl
      );
      return new Response(
        JSON.stringify({
          error: 'Agent service unavailable',
          message:
            'The AI agent is currently unavailable. Please ensure the ADK agent is running and try again.',
          details:
            process.env.NODE_ENV === 'development' ? errorMessage : undefined,
        }),
        {
          status: 503,
          headers: { 'Content-Type': 'application/json' },
        }
      );
    }

    // Handle other errors with generic 500 response
    return new Response(
      JSON.stringify({
        error: 'Internal server error',
        message: 'An error occurred while processing your request.',
        details:
          process.env.NODE_ENV === 'development' ? errorMessage : undefined,
      }),
      {
        status: 500,
        headers: { 'Content-Type': 'application/json' },
      }
    );
  }
};
