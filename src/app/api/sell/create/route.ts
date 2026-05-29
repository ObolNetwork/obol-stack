import { NextRequest, NextResponse } from 'next/server';
import { SellClient } from '@/lib/services/sell';
import { rejectCrossOrigin } from '@/lib/middleware/origin-check';
import {
  normalizeSellToken,
  resolveSellAsset,
} from '@/lib/services/sell-assets';
import type { ServiceOfferType } from '@/types/sell';

export const runtime = 'nodejs';
export const dynamic = 'force-dynamic';

// Defense-in-depth namespace allowlist. The frontend is local-only behind
// the obol.stack hostname restriction so this is not the primary trust
// boundary, but a typo or stale UI state shouldn't be able to drop a CR
// into kube-system / monitoring / x402 / traefik etc. RBAC also limits
// the SA, this is just the earlier failure point.
const ALLOWED_NAMESPACES = new Set<string>(['demo', 'llm']);
const ALLOWED_NAMESPACE_PATTERNS = [
  /^hermes-[a-z0-9-]+$/,
  /^openclaw-[a-z0-9-]+$/,
];

function isAllowedNamespace(ns: string): boolean {
  if (ALLOWED_NAMESPACES.has(ns)) return true;
  return ALLOWED_NAMESPACE_PATTERNS.some(re => re.test(ns));
}

/**
 * POST /api/sell/create
 * Creates a ServiceOffer CR via the Kubernetes API.
 */
export async function POST(request: NextRequest) {
  try {
    const csrfReject = rejectCrossOrigin(request);
    if (csrfReject) return csrfReject;

    const body = await request.json();
    const {
      name,
      namespace,
      type,
      wallet,
      chain,
      token,
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

    const targetNamespace = namespace || 'llm';
    if (!isAllowedNamespace(targetNamespace)) {
      return NextResponse.json(
        {
          error: `Namespace "${targetNamespace}" is not allowed (allowed: demo, llm, hermes-*, openclaw-*).`,
        },
        { status: 400 }
      );
    }

    const client = new SellClient();
    const serviceType = normalizeServiceOfferType(type);
    const paymentToken = normalizeSellToken(token);
    const { asset, error } = resolveSellAsset(chain, paymentToken);
    if (error) {
      return NextResponse.json({ error }, { status: 400 });
    }

    await client.createServiceOffer({
      name,
      namespace: targetNamespace,
      type: serviceType,
      wallet,
      chain,
      token: paymentToken,
      asset,
      pricePerRequest,
      upstream: upstream || 'litellm',
      port: port || 4000,
      healthPath: healthPath || '/health/readiness',
    });

    return NextResponse.json({
      success: true,
      name,
      namespace: targetNamespace,
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

function normalizeServiceOfferType(value: unknown): ServiceOfferType {
  return value === 'inference' || value === 'agent' || value === 'fine-tuning'
    ? value
    : 'http';
}
