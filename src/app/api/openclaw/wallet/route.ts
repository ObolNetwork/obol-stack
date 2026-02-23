import { NextRequest, NextResponse } from 'next/server';
import { OpenClawClient } from '@/lib/services/openclaw';
import type { Web3SignerMetadata } from '@/types/openclaw';

/**
 * GET /api/openclaw/wallet?id=default
 * Returns Web3Signer wallet addresses for an OpenClaw instance.
 * Returns empty addresses array if no wallet-metadata ConfigMap exists.
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
    const namespace = `openclaw-${id}`;
    const metadata = await client.getWeb3SignerMetadata(namespace);

    return NextResponse.json<Web3SignerMetadata>(metadata);
  } catch (error) {
    console.error(`[OpenClaw Wallet] Error for instance '${id}':`, error);
    return NextResponse.json<Web3SignerMetadata>(
      { instanceId: id, addresses: [], count: 0 },
      { status: 500 }
    );
  }
}
