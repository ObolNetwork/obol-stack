import { NextRequest, NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';
import {
  buildDrainPatch,
  parseStopRequest,
  type StopRequestInput,
} from './drain';
import { rejectCrossOrigin } from '@/lib/middleware/origin-check';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * POST /api/sell/stop
 *
 * Initiates a drain on a ServiceOffer by patching spec.drainAt (now) and
 * spec.drainGracePeriod (Go duration string). The CR is intentionally kept
 * around so sellers retain status and audit history after the route is torn
 * down.
 *
 * Body: { name: string, namespace: string, grace?: string, force?: boolean }
 *
 * Returns 200 with { drainEndsAt: ISO } on success, 4xx on validation errors,
 * 500 on K8s API failures.
 */
export async function POST(request: NextRequest) {
  const csrfReject = rejectCrossOrigin(request);
  if (csrfReject) return csrfReject;

  let body: StopRequestInput;
  try {
    body = (await request.json()) as StopRequestInput;
  } catch {
    return NextResponse.json({ error: 'invalid JSON body' }, { status: 400 });
  }

  const parsed = parseStopRequest(body);
  if (!parsed.ok) {
    return NextResponse.json({ error: parsed.error }, { status: 400 });
  }

  const now = new Date();
  const { patch, drainEndsAt } = buildDrainPatch(parsed.value, now);

  try {
    const client = new SellClient();
    await client.patchServiceOffer(
      parsed.value.namespace,
      parsed.value.name,
      patch as unknown as Record<string, unknown>
    );
  } catch (error: any) {
    console.error('[Sell Stop] Error:', error);
    if (error?.statusCode === 404 || error?.response?.statusCode === 404) {
      return NextResponse.json(
        { error: `ServiceOffer "${parsed.value.name}" not found` },
        { status: 404 }
      );
    }
    return NextResponse.json(
      { error: error?.message || 'failed to drain ServiceOffer' },
      { status: 500 }
    );
  }

  return NextResponse.json({
    drainEndsAt: drainEndsAt.toISOString(),
    grace: patch.spec.drainGracePeriod,
  });
}
