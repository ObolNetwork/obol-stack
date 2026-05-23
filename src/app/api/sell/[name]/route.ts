import { NextRequest, NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';
import { rejectCrossOrigin } from '@/lib/middleware/origin-check';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

interface Ctx {
  params: Promise<{ name: string }>;
}

/**
 * PATCH /api/sell/[name]?ns=<namespace>&action=pause|resume
 *
 * Sets or clears the `obol.org/paused` annotation on the ServiceOffer.
 * The controller's reconciliation loop honors the annotation and tears
 * down / republishes the HTTPRoute + Middleware accordingly.
 */
export async function PATCH(req: NextRequest, ctx: Ctx) {
  const csrfReject = rejectCrossOrigin(req);
  if (csrfReject) return csrfReject;

  const { name } = await ctx.params;
  const url = new URL(req.url);
  const namespace = url.searchParams.get('ns');
  const action = url.searchParams.get('action');

  if (!namespace) {
    return NextResponse.json(
      { error: 'missing ?ns=<namespace>' },
      { status: 400 }
    );
  }
  if (action !== 'pause' && action !== 'resume') {
    return NextResponse.json(
      { error: '?action must be "pause" or "resume"' },
      { status: 400 }
    );
  }

  try {
    const client = new SellClient();
    await client.setPaused(name, namespace, action === 'pause');
    return NextResponse.json({
      ok: true,
      name,
      namespace,
      paused: action === 'pause',
    });
  } catch (error) {
    console.error(`[Sell ${action}] Error on ${namespace}/${name}:`, error);
    return NextResponse.json(
      { error: `Failed to ${action} ServiceOffer` },
      { status: 500 }
    );
  }
}

/**
 * DELETE /api/sell/[name]?ns=<namespace>
 *
 * Deletes the ServiceOffer CR. The controller's finalizer handles cascade
 * cleanup of HTTPRoute, Middleware, ReferenceGrant, RegistrationRequest.
 */
export async function DELETE(req: NextRequest, ctx: Ctx) {
  const csrfReject = rejectCrossOrigin(req);
  if (csrfReject) return csrfReject;

  const { name } = await ctx.params;
  const url = new URL(req.url);
  const namespace = url.searchParams.get('ns');

  if (!namespace) {
    return NextResponse.json(
      { error: 'missing ?ns=<namespace>' },
      { status: 400 }
    );
  }

  try {
    const client = new SellClient();
    await client.deleteServiceOffer(name, namespace);
    return NextResponse.json({ ok: true, name, namespace, deleted: true });
  } catch (error) {
    console.error(`[Sell delete] Error on ${namespace}/${name}:`, error);
    return NextResponse.json(
      { error: 'Failed to delete ServiceOffer' },
      { status: 500 }
    );
  }
}
