import { NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/sell/list
 * Lists all ServiceOffer CRs.
 */
export async function GET() {
  try {
    const client = new SellClient();
    const offers = await client.listServiceOffers();
    return NextResponse.json({ offers });
  } catch (error) {
    console.error('[Sell List] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list ServiceOffers' },
      { status: 500 }
    );
  }
}
