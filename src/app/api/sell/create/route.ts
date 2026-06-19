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

const ALLOWED_NAMESPACES = new Set<string>(['demo', 'llm']);
const ALLOWED_NAMESPACE_PATTERNS = [
  /^hermes-[a-z0-9-]+$/,
  /^openclaw-[a-z0-9-]+$/,
  /^agent-[a-z0-9-]+$/,
];

function isAllowedNamespace(ns: string): boolean {
  if (ALLOWED_NAMESPACES.has(ns)) return true;
  return ALLOWED_NAMESPACE_PATTERNS.some(re => re.test(ns));
}

/**
 * POST /api/sell/create
 * Creates a ServiceOffer CR via the Kubernetes API.
 *
 * Full UI parity gaps: obol-stack `plans/frontend-sell-ui-gaps.md`.
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
      model,
      agentRef,
      skills,
      description,
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

    const serviceType = normalizeServiceOfferType(type);

    if (serviceType === 'inference' && !model) {
      return NextResponse.json(
        { error: 'Missing required field for inference listings: model' },
        { status: 400 }
      );
    }

    if (serviceType === 'agent') {
      const refName = agentRef?.name ?? name;
      const refNs = agentRef?.namespace ?? `agent-${refName}`;
      if (!refName || !refNs) {
        return NextResponse.json(
          { error: 'Missing agentRef for agent listings' },
          { status: 400 }
        );
      }
    }

    const isInference = serviceType === 'inference';
    const isAgent = serviceType === 'agent';
    const targetNamespace =
      namespace || (isAgent ? `agent-${agentRef?.name ?? name}` : 'llm');

    if (!isAllowedNamespace(targetNamespace)) {
      return NextResponse.json(
        {
          error: `Namespace "${targetNamespace}" is not allowed (allowed: demo, llm, hermes-*, openclaw-*, agent-*).`,
        },
        { status: 400 }
      );
    }

    const client = new SellClient();
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
      model: isInference ? String(model).trim() : undefined,
      agentRef: isAgent
        ? {
            name: agentRef?.name ?? name,
            namespace: agentRef?.namespace ?? targetNamespace,
          }
        : undefined,
      skills: isAgent && Array.isArray(skills) ? skills : undefined,
      description: isAgent && description ? String(description) : undefined,
      upstream: isAgent
        ? undefined
        : isInference
          ? name
          : upstream || 'litellm',
      port: isAgent ? undefined : isInference ? 8402 : port || 4000,
      healthPath: isAgent
        ? undefined
        : isInference
          ? '/health'
          : healthPath || '/health/readiness',
    });

    return NextResponse.json({
      success: true,
      name,
      namespace: targetNamespace,
      type: serviceType,
    });
  } catch (error: any) {
    console.error('[Sell Create] Error:', error);

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
