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
    const page = parseInt(request.nextUrl.searchParams.get('page') || '1', 10);
    const search = request.nextUrl.searchParams.get('search') || undefined;
    const protocol = request.nextUrl.searchParams.get('protocol') || undefined;
    const chainIdParam = request.nextUrl.searchParams.get('chainId');
    const chainId = chainIdParam ? parseInt(chainIdParam, 10) : undefined;
    const x402Supported =
      request.nextUrl.searchParams.get('x402_supported') === 'true' ||
      undefined;
    const client = new RegistryClient();
    const { agents, total, hasMore } = await client.listAgents(
      limit,
      page,
      search,
      protocol,
      chainId,
      x402Supported
    );
    return NextResponse.json({ agents, total, hasMore });
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
