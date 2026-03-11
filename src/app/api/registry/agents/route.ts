import { NextRequest, NextResponse } from 'next/server';
import { RegistryClient } from '@/lib/services/registry';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/registry/agents?limit=20&search=...&protocol=MCP
 */
export async function GET(request: NextRequest) {
  try {
    const limit = parseInt(
      request.nextUrl.searchParams.get('limit') || '20',
      10
    );
    const search = request.nextUrl.searchParams.get('search') || undefined;
    const protocol = request.nextUrl.searchParams.get('protocol') || undefined;
    const client = new RegistryClient();
    const agents = await client.listAgents(limit, search, protocol);
    return NextResponse.json({ agents });
  } catch (error) {
    console.error('[Registry Agents] Error:', error);
    return NextResponse.json(
      {
        error:
          error instanceof Error ? error.message : 'Failed to query registry',
      },
      { status: 500 }
    );
  }
}
