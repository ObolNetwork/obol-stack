/**
 * Display-status derivation for a seller's own ServiceOffer dashboard card.
 *
 * The controller has two tiers of readiness:
 * - Ready includes Registered and drives offer.status === 'Ready'.
 * - Operational readiness excludes Registered and is enough to serve paid x402
 *   traffic and list the offer in the catalog.
 */
import type { ServiceOffer, ServiceOfferCondition } from '@/types/sell';

export type OfferDisplayStatus =
  | 'draining'
  | 'ready'
  | 'registration-pending'
  | 'pending';

const OPERATIONAL_CONDITIONS = [
  'ModelReady',
  'UpstreamHealthy',
  'PaymentGateReady',
  'RoutePublished',
] as const;

function conditionIsTrue(
  conditions: ServiceOfferCondition[] | undefined,
  type: string
): boolean {
  return conditions?.some(c => c.type === type && c.status === 'True') ?? false;
}

export function isOperationallyReady(offer: ServiceOffer): boolean {
  return OPERATIONAL_CONDITIONS.every(type => {
    if (type === 'ModelReady' && offer.type === 'http') return true;
    return conditionIsTrue(offer.conditions, type);
  });
}

export function isRegistrationPending(offer: ServiceOffer): boolean {
  if (!offer.registration?.enabled) return false;
  const c = offer.conditions?.find(x => x.type === 'Registered');
  return c !== undefined && c.status !== 'True';
}

export function registeredConditionMessage(
  offer: ServiceOffer
): string | undefined {
  const msg = offer.conditions?.find(x => x.type === 'Registered')?.message;
  return msg && msg.trim() !== '' ? msg : undefined;
}

export function deriveOfferDisplayStatus(
  offer: ServiceOffer,
  opts: { isDraining: boolean }
): OfferDisplayStatus {
  if (opts.isDraining) return 'draining';
  if (offer.status === 'Ready') return 'ready';
  if (isOperationallyReady(offer) && isRegistrationPending(offer)) {
    return 'registration-pending';
  }
  return 'pending';
}
