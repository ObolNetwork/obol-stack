import { NextRequest, NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import type { AgentInstanceHealth, AgentRuntime } from '@/types/agent';
import { logSafe } from '@/lib/utils/log-safe';

export async function GET(request: NextRequest) {
  const id = request.nextUrl.searchParams.get('id');
  const namespace = request.nextUrl.searchParams.get('namespace');
  const runtime = request.nextUrl.searchParams.get(
    'runtime'
  ) as AgentRuntime | null;

  if (!id || !runtime) {
    return NextResponse.json(
      { error: 'Missing runtime or id parameter' },
      { status: 400 }
    );
  }

  if (runtime !== 'hermes') {
    return NextResponse.json(
      { error: `Unsupported runtime '${runtime}'` },
      { status: 400 }
    );
  }

  try {
    const client = new AgentClient();
    const health = await client.getInstanceHealth(
      runtime,
      id,
      namespace ?? undefined
    );

    if (health.error?.includes('not found')) {
      return NextResponse.json<AgentInstanceHealth>(health, { status: 404 });
    }

    return NextResponse.json<AgentInstanceHealth>(health);
  } catch (error) {
    console.error('[Agent Status] Error for %s/%s:', logSafe(runtime), logSafe(id), error);

    return NextResponse.json<AgentInstanceHealth>(
      {
        id,
        runtime,
        healthy: false,
        model: 'unknown',
        endpoint: '',
        error: error instanceof Error ? error.message : 'Unknown error',
      },
      { status: 500 }
    );
  }
}
