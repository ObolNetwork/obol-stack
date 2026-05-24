import { NextResponse } from 'next/server';
import { PurchasesClient } from '@/lib/services/purchases';
import type { PurchaseListResponse } from '@/types/purchase';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * GET /api/marketplace/purchases
 * Lists all PurchaseRequest CRs across the cluster. The frontend's
 * MyPurchasesPage filters client-side by the connected wallet (matched
 * against status.signerAddress).
 */
export async function GET() {
  try {
    const client = new PurchasesClient();
    const purchases = await client.listPurchaseRequests();
    const body: PurchaseListResponse = { purchases };
    return NextResponse.json(body);
  } catch (error) {
    console.error('[Purchases] Error:', error);
    return NextResponse.json(
      { error: 'Failed to list PurchaseRequests' },
      { status: 500 }
    );
  }
}
