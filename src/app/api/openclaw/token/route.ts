import { NextRequest, NextResponse } from 'next/server';
import { rejectCrossOriginCredentialRead } from '@/lib/middleware/origin-check';
import { OpenClawClient } from '@/lib/services/openclaw';

/**
 * GET /api/openclaw/token?id=default
 * Returns the gateway token for an OpenClaw instance.
 * This must never be embedded in dashboard URLs.
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
    const token = await client.getGatewayToken(namespace);

    if (!token) {
      return NextResponse.json({ error: 'Token not found' }, { status: 404 });
    }

    return NextResponse.json({ token });
  } catch (error) {
    console.error(`[OpenClaw Token] Error for instance '${id}':`, error);
    return NextResponse.json({ error: 'Failed to get token' }, { status: 500 });
  }
}
