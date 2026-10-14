import type { ServiceOffer } from '@/types/sell';
import type { AgentInstance } from '@/types/agent';

export function formatUsageCount(value: number | null | undefined): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/** Display string for usage metrics — em dash when Prom returned no value for this offer. */
export function formatUsageDisplay(value: number | null | undefined): string {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return value.toLocaleString();
  }
  return '—';
}

/**
 * Offers linked to an agent instance.
 * Legacy http/inference rows without `agentLink` are only included when
 * `receiveLegacyOrphans` is true (sole default Hermes primary on the dashboard).
 */
export function offersForAgent(
  offers: ServiceOffer[],
  agent: Pick<AgentInstance, 'runtime' | 'id' | 'isSubAgent'>,
  options?: { receiveLegacyOrphans?: boolean }
): ServiceOffer[] {
  const receiveLegacy = options?.receiveLegacyOrphans === true;
  return offers.filter(offer => {
    if (offer.agentLink) {
      return (
        offer.agentLink.runtime === agent.runtime &&
        offer.agentLink.id === agent.id
      );
    }
    return receiveLegacy;
  });
}

/**
 * Charged requests in the last 24h (`usage.requests24h` from `/api/sell/list`,
 * sourced from `chargedRequests24hByOffer` PromQL).
 */
export function sumChargedRequests24h(offers: ServiceOffer[]): number {
  return offers.reduce((sum, offer) => {
    const n = offer.usage?.requests24h;
    return sum + (typeof n === 'number' && Number.isFinite(n) ? n : 0);
  }, 0);
}

export function sumChargedRequests24hForAgent(
  offers: ServiceOffer[],
  agent: Pick<AgentInstance, 'runtime' | 'id' | 'isSubAgent'>,
  options?: { receiveLegacyOrphans?: boolean }
): number {
  return sumChargedRequests24h(offersForAgent(offers, agent, options));
}

/** @deprecated Use sumChargedRequests24h — kept for older imports. */
export const sumRequests24h = sumChargedRequests24h;

/** @deprecated Use sumChargedRequests24hForAgent */
export const sumRequests24hForAgent = sumChargedRequests24hForAgent;

export function sumChargedRequests7d(offers: ServiceOffer[]): number {
  return offers.reduce((sum, offer) => {
    const n = offer.usage?.chargedRequests;
    return sum + (typeof n === 'number' && Number.isFinite(n) ? n : 0);
  }, 0);
}

/** @deprecated Use sumChargedRequests7d */
export const sumChargedRequests = sumChargedRequests7d;

export function formatRelativeAgo(
  unixSeconds: number | null | undefined
): string {
  if (!unixSeconds || unixSeconds <= 0 || !Number.isFinite(unixSeconds)) {
    return '—';
  }
  const ageSec = Math.max(0, Math.floor(Date.now() / 1000 - unixSeconds));
  if (ageSec < 60) return `${ageSec}s ago`;
  if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`;
  if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`;
  return `${Math.floor(ageSec / 86400)}d ago`;
}
