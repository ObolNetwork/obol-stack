import { NextRequest, NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

/**
 * POST /api/sell/create
 * Creates a ServiceOffer CR via the Kubernetes API.
 */
export async function POST(request: NextRequest) {
  try {
    const body = await request.json();
    const {
      name,
      namespace,
      wallet,
      chain,
      pricePerRequest,
      upstream,
      port,
      healthPath,
    } = body;

    if (!name || !wallet || !chain || !pricePerRequest) {
      return NextResponse.json(
        {
          error:
            'Missing required fields: name, wallet, chain, pricePerRequest',
        },
        { status: 400 }
      );
    }

    const client = new SellClient();
    await client.createServiceOffer({
      name,
      namespace: namespace || 'llm',
      wallet,
      chain,
      pricePerRequest,
      upstream: upstream || 'litellm',
      port: port || 4000,
      healthPath: healthPath || '/health/readiness',
    });

    return NextResponse.json({
      success: true,
      name,
      namespace: namespace || 'llm',
    });
  } catch (error: any) {
    console.error('[Sell Create] Error:', error);

    // K8s 409 = already exists
    if (error?.statusCode === 409 || error?.response?.statusCode === 409) {
      return NextResponse.json(
        {
          error: `ServiceOffer "${(await request.clone().json()).name}" already exists`,
        },
        { status: 409 }
      );
    }

    return NextResponse.json(
      { error: error?.message || 'Failed to create ServiceOffer' },
      { status: 500 }
    );
  }
}
