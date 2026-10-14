import { NextRequest, NextResponse } from 'next/server';
import { AgentClient, isDns1123Label } from '@/lib/services/agents';
import type { AgentRuntime } from '@/types/agent';
import { rejectCrossOrigin } from '@/lib/middleware/origin-check';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

const DEFAULT_HERMES_INSTANCE_ID = 'obol-agent';

export async function POST(request: NextRequest) {
  try {
    const csrfReject = rejectCrossOrigin(request);
    if (csrfReject) return csrfReject;

    const body = await request.json();
    const runtime = (body.runtime || 'hermes') as AgentRuntime;
    const instanceId =
      body.instanceId ||
      (runtime === 'hermes' ? DEFAULT_HERMES_INSTANCE_ID : '');
    const {
      messages,
      stream = false,
      sessionId,
      runtime: _runtime,
      instanceId: _instanceId,
      ...otherParams
    } = body;
    void _runtime;
    void _instanceId;

    if (typeof instanceId !== 'string' || !isDns1123Label(instanceId)) {
      return NextResponse.json(
        { error: 'instanceId is required and must be a DNS-1123 label' },
        { status: 400 }
      );
    }

    if (runtime !== 'hermes') {
      return NextResponse.json(
        { error: `Unsupported runtime '${runtime}'` },
        { status: 400 }
      );
    }

    if (!messages || !Array.isArray(messages)) {
      return NextResponse.json(
        { error: 'messages array is required' },
        { status: 400 }
      );
    }

    const client = new AgentClient();
    const instance = client.resolveInstance(runtime, instanceId);
    if (!instance) {
      return NextResponse.json(
        { error: `Instance '${instanceId}' not found` },
        { status: 404 }
      );
    }

    const token = await client.getGatewayToken(instance);
    if (!token) {
      return NextResponse.json(
        { error: 'Failed to get agent API token' },
        { status: 500 }
      );
    }

    const url = client.getServiceUrl(instance);
    const health = await client.getInstanceHealth(runtime, instanceId);
    const model =
      health.model && health.model !== 'unknown'
        ? health.model
        : runtime === 'hermes'
          ? 'hermes-agent'
          : 'openai/gpt-oss:120b-cloud';

    const headers: HeadersInit = {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
    };

    if (
      runtime === 'hermes' &&
      typeof sessionId === 'string' &&
      sessionId.trim()
    ) {
      headers['X-Hermes-Session-Id'] = sessionId.trim();
    }

    const agentResponse = await fetch(`${url}/v1/chat/completions`, {
      method: 'POST',
      headers,
      body: JSON.stringify({
        model,
        messages,
        stream,
        ...otherParams,
      }),
    });

    if (!agentResponse.ok) {
      const errorText = await agentResponse.text();
      return NextResponse.json(
        {
          error: `${runtime} API error`,
          details: errorText,
          status: agentResponse.status,
        },
        { status: agentResponse.status }
      );
    }

    const hermesSessionId =
      runtime === 'hermes'
        ? agentResponse.headers.get('X-Hermes-Session-Id')
        : null;

    if (stream) {
      const passthroughHeaders: HeadersInit = {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      };

      if (hermesSessionId) {
        passthroughHeaders['X-Hermes-Session-Id'] = hermesSessionId;
      }

      return new NextResponse(agentResponse.body, {
        headers: passthroughHeaders,
      });
    }

    const data = await agentResponse.json();
    const response = NextResponse.json(data);
    if (hermesSessionId) {
      response.headers.set('X-Hermes-Session-Id', hermesSessionId);
    }
    return response;
  } catch (error) {
    console.error('[Agent Chat] Error:', error);
    return NextResponse.json(
      {
        error: 'Failed to process chat request',
        details: error instanceof Error ? error.message : 'Unknown error',
      },
      { status: 500 }
    );
  }
}
