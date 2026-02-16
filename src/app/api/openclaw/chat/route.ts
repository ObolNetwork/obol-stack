import { NextRequest, NextResponse } from 'next/server';
import { OpenClawClient } from '@/lib/services/openclaw';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * POST /api/openclaw/chat
 * Proxies chat requests to OpenClaw instances
 *
 * Body:
 * {
 *   "instanceId": "default",
 *   "messages": [{"role": "user", "content": "Hello!"}],
 *   "stream": false
 * }
 */
export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const { instanceId, messages, stream = false, ...otherParams } = body;

    if (!instanceId) {
      return NextResponse.json(
        { error: 'instanceId is required' },
        { status: 400 }
      );
    }

    if (!messages || !Array.isArray(messages)) {
      return NextResponse.json(
        { error: 'messages array is required' },
        { status: 400 }
      );
    }

    const openclawClient = new OpenClawClient();
    const namespace = `openclaw-${instanceId}`;

    // Get gateway token
    const token = await openclawClient.getGatewayToken(namespace);
    if (!token) {
      return NextResponse.json(
        { error: 'Failed to get gateway token' },
        { status: 500 }
      );
    }

    // Get service URL
    const url = openclawClient.getServiceUrl(instanceId);

    // Get model from config
    const configInfo = await openclawClient.getConfigInfo(namespace);
    const model = configInfo.model || 'ollama/gpt-oss:120b-cloud';

    // Forward request to OpenClaw
    const openclawResponse = await fetch(`${url}/v1/chat/completions`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        model,
        messages,
        stream,
        ...otherParams,
      }),
    });

    if (!openclawResponse.ok) {
      const errorText = await openclawResponse.text();
      return NextResponse.json(
        {
          error: 'OpenClaw API error',
          details: errorText,
          status: openclawResponse.status,
        },
        { status: openclawResponse.status }
      );
    }

    // Stream response if requested
    if (stream) {
      return new NextResponse(openclawResponse.body, {
        headers: {
          'Content-Type': 'text/event-stream',
          'Cache-Control': 'no-cache',
          Connection: 'keep-alive',
        },
      });
    }

    // Non-streaming response
    const data = await openclawResponse.json();
    return NextResponse.json(data);
  } catch (error) {
    console.error('[OpenClaw Chat] Error:', error);
    return NextResponse.json(
      {
        error: 'Failed to process chat request',
        details: error instanceof Error ? error.message : 'Unknown error',
      },
      { status: 500 }
    );
  }
}
