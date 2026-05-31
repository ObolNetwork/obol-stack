import { NextRequest, NextResponse } from 'next/server';
import { rejectCrossOriginCredentialRead } from '@/lib/middleware/origin-check';
import { OpenClawClient } from '@/lib/services/openclaw';
import type { WalletMetadata } from '@/types/openclaw';

/**
 * GET /api/openclaw/wallet?id=default
 * Returns wallet signing addresses for an OpenClaw instance.
 * Returns empty addresses array if no wallet-metadata ConfigMap exists.
 */
export async function GET(request: NextRequest) {
  const csrfReject = rejectCrossOriginCredentialRead(request);
  if (csrfReject) return csrfReject;

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
    const metadata = await client.getWalletMetadata(namespace);

    return NextResponse.json<WalletMetadata>(metadata);
  } catch (error) {
    console.error(`[OpenClaw Wallet] Error for instance '${id}':`, error);
    return NextResponse.json<WalletMetadata>(
      { instanceId: id, addresses: [], count: 0 },
      { status: 500 }
    );
  }
}
