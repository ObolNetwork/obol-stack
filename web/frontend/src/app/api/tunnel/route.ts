import { NextResponse } from 'next/server';
import { AgentClient } from '@/lib/services/agents';

/**
 * GET /api/tunnel
 * Returns the tunnel URL from the obol-stack-config ConfigMap.
 * Written by the CLI during `obol tunnel status/login/provision`.
 */
export async function GET() {
  try {
    const client = new AgentClient();
    const tunnelURL = await client.getTunnelURL();

    return NextResponse.json({ tunnelURL });
  } catch (error) {
    console.error('[Tunnel] Error fetching tunnel URL:', error);
    return NextResponse.json(
      { status: 500, error: 'Failed to fetch tunnel URL' },
      { status: 500 }
    );
  }
}
