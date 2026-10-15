import { NextRequest, NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import { rejectCrossOriginCredentialRead } from '@/lib/middleware/origin-check';
import type { AgentRuntime } from '@/types/agent';
import { logSafe } from '@/lib/utils/log-safe';

export async function GET(request: NextRequest) {
  const csrfReject = rejectCrossOriginCredentialRead(request);
  if (csrfReject) return csrfReject;

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
    const instance = namespace
      ? client.resolveInstanceByNamespace(namespace)
      : client.resolveInstance(runtime, id);
    if (!instance) {
      return NextResponse.json(
        { error: 'Instance not found' },
        { status: 404 }
      );
    }

    const token = await client.getGatewayToken(instance);
    if (!token) {
      return NextResponse.json({ error: 'Token not found' }, { status: 404 });
    }

    return NextResponse.json({ token });
  } catch (error) {
    console.error('[Agent Token] Error for %s/%s:', logSafe(runtime), logSafe(id), error);
    return NextResponse.json({ error: 'Failed to get token' }, { status: 500 });
  }
}
