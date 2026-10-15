import { NextRequest, NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';
import { rejectCrossOriginCredentialRead } from '@/lib/middleware/origin-check';
import type { AgentInstance, AgentRuntime } from '@/types/agent';
import type { WalletMetadata } from '@/types/wallet';
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
    const instance: AgentInstance | null = namespace
      ? client.resolveInstanceByNamespace(namespace)
      : client.resolveInstance(runtime, id);
    if (!instance) {
      return NextResponse.json(
        { error: `Instance '${id}' not found` },
        { status: 404 }
      );
    }

    const metadata = await client.getWalletMetadata(instance);
    return NextResponse.json<WalletMetadata>(metadata);
  } catch (error) {
    console.error('[Agent Wallet] Error for %s/%s:', logSafe(runtime), logSafe(id), error);
    return NextResponse.json<WalletMetadata>(
      { instanceId: id, addresses: [], count: 0 },
      { status: 500 }
    );
  }
}
