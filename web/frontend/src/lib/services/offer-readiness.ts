import type { ServiceOffer, ServiceOfferCondition } from '@/types/sell';

export const READINESS_STEPS: Array<{
  key: string;
  short: string;
  summary: string;
}> = [
  {
    key: 'ModelReady',
    short: 'Model',
    summary: 'Model or runtime is configured and ready to serve traffic.',
  },
  {
    key: 'UpstreamHealthy',
    short: 'Upstream',
    summary:
      'Backend passed its health check (Ollama, Hermes agent, HTTP upstream, etc.).',
  },
  {
    key: 'PaymentGateReady',
    short: 'Gate',
    summary: 'x402 payment gate is wired on this route.',
  },
  {
    key: 'RoutePublished',
    short: 'Route',
    summary: 'Traefik published the public /services/<name> path.',
  },
  {
    key: 'Registered',
    short: 'Registered',
    summary: 'ERC-8004 on-chain identity registration completed.',
  },
];

export type ReadinessState = 'ok' | 'pending' | 'missing';

export function readinessState(
  conditions: ServiceOfferCondition[] | undefined,
  type: string
): ReadinessState {
  const c = conditions?.find(x => x.type === type);
  if (!c) return 'missing';
  return c.status === 'True' ? 'ok' : 'pending';
}

export function readinessStepsForOffer(offer: ServiceOffer) {
  return READINESS_STEPS.filter(({ key }) => {
    if (key === 'ModelReady' && offer.type === 'http') return false;
    if (key === 'Registered' && !offer.registration?.enabled) return false;
    return true;
  });
}

export function readinessTooltipLines(
  key: string,
  summary: string,
  conditions: ServiceOfferCondition[] | undefined
): { title: string; summary: string; message?: string } {
  const state = readinessState(conditions, key);
  const stateLabel =
    state === 'ok' ? 'Ready' : state === 'pending' ? 'Pending' : 'Not reported';
  const message = conditions?.find(x => x.type === key)?.message?.trim();
  return {
    title: `${key.replace(/([A-Z])/g, ' $1').trim()} — ${stateLabel}`,
    summary,
    message: message || undefined,
  };
}
