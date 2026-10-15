import type { AgentInstance, AgentListing } from '../../types/agent.ts';
import type { ServiceOffer } from '../../types/sell.ts';
import {
  deriveOfferDisplayStatus,
  isOperationallyReady,
} from './offer-status.ts';
import { offerKey } from './sell-catalog.ts';

/** DOM anchor for a ServiceOffer row (ServiceList + ServiceCard). */
export function offerAnchorId(namespace: string, name: string): string {
  return `offer-${namespace}-${name}`.replace(/\//g, '_');
}

export function offerMatchesAgent(
  offer: ServiceOffer,
  instance: AgentInstance
): boolean {
  if (offer.type !== 'agent') return false;

  const ref = offer.agent?.ref;
  const refNs = ref?.namespace?.trim();
  const refName = ref?.name?.trim();

  if (refNs) {
    if (instance.namespace !== refNs) return false;
    if (refName && instance.id !== refName) return false;
    return true;
  }

  return offer.namespace === instance.namespace;
}

export function findAgentOffer(
  instance: AgentInstance,
  offers: ServiceOffer[]
): ServiceOffer | null {
  if (!instance.isSubAgent) return null;
  return offers.find(o => offerMatchesAgent(o, instance)) ?? null;
}

export function buildAgentListing(
  offer: ServiceOffer,
  inCatalog: boolean
): AgentListing {
  const isDraining = Boolean(offer.drainEndsAt?.trim());
  const displayStatus = deriveOfferDisplayStatus(offer, { isDraining });

  let phase: AgentListing['phase'] = 'setting_up';
  if (isDraining || displayStatus === 'draining') {
    phase = 'draining';
  } else if (inCatalog && isOperationallyReady(offer)) {
    phase = 'live';
  }

  return {
    offerNamespace: offer.namespace,
    offerName: offer.name,
    phase,
    displayStatus,
    inCatalog,
    endpoint: offer.endpoint,
    anchorId: offerAnchorId(offer.namespace, offer.name),
  };
}

export function attachAgentListings(
  instances: AgentInstance[],
  liveOffers: ServiceOffer[],
  catalogKeys: Set<string>
): AgentInstance[] {
  return instances.map(instance => {
    if (!instance.isSubAgent) return instance;
    const offer = findAgentOffer(instance, liveOffers);
    if (!offer) return instance;
    const inCatalog = catalogKeys.has(offerKey(offer.namespace, offer.name));
    return {
      ...instance,
      listing: buildAgentListing(offer, inCatalog),
    };
  });
}

/** CRs that exist but are not yet in the public catalog. */
export function splitPendingOffers(
  liveOffers: ServiceOffer[],
  catalogKeys: Set<string>
): ServiceOffer[] {
  return liveOffers.filter(offer => {
    const key = offerKey(offer.namespace, offer.name);
    if (catalogKeys.has(key)) return false;
    return offer.type === 'agent';
  });
}
