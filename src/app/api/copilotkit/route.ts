import {
  CopilotRuntime,
  ExperimentalEmptyAdapter,
  copilotRuntimeNextJSAppRouterEndpoint,
} from '@copilotkit/runtime';
import { HttpAgent } from '@ag-ui/client';
import { NextRequest } from 'next/server';

const serviceAdapter = new ExperimentalEmptyAdapter();

const runtime = new CopilotRuntime({
  agents: {
    obol_agent: new HttpAgent({
      url: process.env.ADK_AGENT_URL || 'http://127.0.0.1:8000/',
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
    console.log('[CopilotKit Route] Response status:', response.status);
    return response;
  } catch (error) {
    console.error('[CopilotKit Route] Error:', error);
    throw error;
  }
};
