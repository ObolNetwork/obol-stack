import { NextRequest, NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import type { AgentInstance, AgentRuntime } from '@/types/agent';

export async function GET(request: NextRequest) {
  const id = request.nextUrl.searchParams.get('id');
  const runtime = request.nextUrl.searchParams.get(
    'runtime'
  ) as AgentRuntime | null;

  if (!id || !runtime) {
    return NextResponse.json(
      { error: 'Missing runtime or id parameter' },
      { status: 400 }
    );
  }

  if (runtime !== 'hermes' && runtime !== 'openclaw') {
    return NextResponse.json(
      { error: `Unsupported runtime '${runtime}'` },
      { status: 400 }
    );
  }

  try {
    const client = new AgentClient();
    const instance: AgentInstance = {
      id,
      runtime,
      namespace: `${runtime}-${id}`,
      hostname: `${runtime}-${id}`,
      status: 'unknown',
    };

    const token = await client.getGatewayToken(instance);
    if (!token) {
      return NextResponse.json({ error: 'Token not found' }, { status: 404 });
    }

    return NextResponse.json({ token });
  } catch (error) {
    console.error(`[Agent Token] Error for ${runtime}/${id}:`, error);
    return NextResponse.json({ error: 'Failed to get token' }, { status: 500 });
  }
}
