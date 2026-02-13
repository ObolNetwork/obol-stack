import { NextRequest, NextResponse } from 'next/server';
import { OpenClawClient } from '@/lib/services/openclaw';
import type { OpenClawInstanceHealth } from '@/types/openclaw';

/**
 * GET /api/openclaw/status?id=default
 * Gets health status for a specific OpenClaw instance
 */
export async function GET(request: NextRequest) {
  const id = request.nextUrl.searchParams.get('id');

  if (!id) {
    return NextResponse.json(
      { error: 'Missing id parameter' },
      { status: 400 }
    );
  }

  try {
    const client = new OpenClawClient();
    const health = await client.getInstanceHealth(id);

    if (health.error?.includes('not found')) {
      return NextResponse.json<OpenClawInstanceHealth>(health, { status: 404 });
    }

    return NextResponse.json<OpenClawInstanceHealth>(health);
  } catch (error) {
    console.error(`[OpenClaw Status] Error for instance '${id}':`, error);

    return NextResponse.json<OpenClawInstanceHealth>(
      {
        id,
        healthy: false,
        model: 'unknown',
        endpoint: '',
        error: error instanceof Error ? error.message : 'Unknown error',
      },
      { status: 500 }
    );
  }
}
