import type { ServiceOfferType } from '@/types/sell';

export interface Probe402Result {
  /** Request completed without transport error. */
  ok: boolean;
  httpStatus: number;
  /** True when the gateway returned HTTP 402 (payment gate is working). */
  gated: boolean;
  summary: string;
  acceptsCount: number;
  priceHint?: string;
  network?: string;
  asset?: string;
  error?: string;
}

export function buildServiceProbeUrl(
  storefrontBase: string,
  offer: {
    name: string;
    type?: ServiceOfferType | string;
    model?: { name?: string };
    agentResolution?: { model?: string };
  }
): string {
  const base = `${storefrontBase.replace(/\/+$/, '')}/services/${offer.name}`;
  if (offer.type === 'http') {
    return `${base}/`;
  }
  return `${base}/v1/chat/completions`;
}

export function chatProbeBody(modelName: string): Record<string, unknown> {
  return {
    model: modelName,
    messages: [{ role: 'user', content: 'probe' }],
    max_tokens: 1,
    stream: false,
  };
}

export function probeModelForOffer(offer: {
  model?: { name?: string };
  agentResolution?: { model?: string };
  name: string;
}): string {
  return offer.agentResolution?.model ?? offer.model?.name ?? offer.name;
}

/** Parse an unpaid probe response — success means we got 402 + payment requirements. */
export function parseProbeResponse(
  status: number,
  bodyText: string
): Probe402Result {
  if (status === 402) {
    let acceptsCount = 0;
    let priceHint: string | undefined;
    let network: string | undefined;
    let asset: string | undefined;

    try {
      const json = JSON.parse(bodyText) as {
        accepts?: Array<{
          maxAmountRequired?: string;
          network?: string;
          asset?: string | { symbol?: string };
        }>;
      };
      const accepts = json.accepts ?? [];
      acceptsCount = accepts.length;
      const first = accepts[0];
      if (first?.maxAmountRequired) priceHint = first.maxAmountRequired;
      if (first?.network) network = first.network;
      const a = first?.asset;
      if (typeof a === 'string') asset = a;
      else if (a?.symbol) asset = a.symbol;
    } catch {
      // Body may be non-JSON; 402 alone is still a pass for gating.
    }

    const parts = ['Payment gate OK (402)'];
    if (priceHint) parts.push(`amount ${priceHint}`);
    if (asset) parts.push(asset);
    if (network) parts.push(network);

    return {
      ok: true,
      httpStatus: 402,
      gated: true,
      summary: parts.join(' · '),
      acceptsCount,
      priceHint,
      network,
      asset,
    };
  }

  if (status >= 200 && status < 300) {
    return {
      ok: true,
      httpStatus: status,
      gated: false,
      summary: `Got ${status} without payment — gate may be off or misconfigured`,
      acceptsCount: 0,
    };
  }

  return {
    ok: false,
    httpStatus: status,
    gated: false,
    summary: `Unexpected HTTP ${status}`,
    acceptsCount: 0,
    error: bodyText.slice(0, 200) || undefined,
  };
}
