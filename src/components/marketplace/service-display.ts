// Shared presentation helpers for marketplace listings, so the Cards grid and
// the List table render identical badges, chain labels, status tones, price
// units, and storefront links. Pure (no JSX) — components own the markup.

import { isErc8004Listing } from '@/lib/marketplace/types';
import type { ServiceListItem } from '@/lib/marketplace/types';
import type { ChainName } from '@/types/dashboard';
import type { ServiceOfferType } from '@/types/sell';

// Public registry explorer — the storefront equivalent for remote ERC-8004
// listings, which aren't served by this cluster's storefront.
export const SCAN_URL = 'https://www.8004scan.io';

// Darker-than-default label for filled-green primary buttons (Storefront,
// New listing) so the text reads clearly against $obolGreen — the primary
// variant's own colour is a mid-green with weak contrast.
export const STOREFRONT_TEXT_CSS = { color: '#062018' } as const;

export const BADGE_VARIANT: Record<
  ServiceOfferType,
  'purple' | 'code' | 'neutral'
> = {
  agent: 'purple',
  inference: 'purple',
  http: 'code',
  'fine-tuning': 'neutral',
};

export const BADGE_LABEL: Record<ServiceOfferType, string> = {
  agent: 'Agent',
  inference: 'Inference',
  http: 'HTTP',
  'fine-tuning': 'Fine-tune',
};

export const PRICE_UNIT_LABEL: Record<ServiceListItem['priceUnit'], string> = {
  request: 'per request',
  'agent-turn': 'per turn',
  mtok: 'per Mtok',
  hour: 'per hour',
  epoch: 'per epoch',
};

export type StatusTone = 'live' | 'pending' | 'degraded';

export function statusTone(status: ServiceListItem['status']): StatusTone {
  switch (status) {
    case 'live':
      return 'live';
    case 'pending-registration':
      return 'pending';
    case 'degraded':
    default:
      return 'degraded';
  }
}

export function statusLabel(status: ServiceListItem['status']): string {
  switch (status) {
    case 'live':
      return 'Live';
    case 'pending-registration':
      return 'Pending';
    case 'degraded':
      return 'Degraded';
  }
}

export function chainName(item: ServiceListItem): ChainName | undefined {
  switch (item.network.caip2) {
    case 'eip155:1':
      return 'mainnet';
    case 'eip155:11155111':
      return 'sepolia';
    case 'eip155:8453':
      return 'base';
    case 'eip155:84532':
      return 'base-sepolia';
    default:
      return undefined;
  }
}

export function chainLabel(item: ServiceListItem): string {
  switch (item.network.caip2) {
    case 'eip155:1':
      return 'Ethereum';
    case 'eip155:11155111':
      return 'Sepolia';
    case 'eip155:8453':
      return 'Base';
    case 'eip155:84532':
      return 'Base Sepolia';
    default:
      return item.network.alias || item.network.caip2;
  }
}

export function skillMdUrl(endpoint: string): string | undefined {
  try {
    const u = new URL(endpoint);
    u.pathname = '/skill.md';
    u.search = '';
    u.hash = '';
    return u.toString();
  } catch {
    return undefined;
  }
}

/** Where the "Storefront" action points: 8004scan for remote ERC-8004
 *  listings, otherwise the operator's public storefront root. */
export function storefrontHref(
  item: ServiceListItem,
  storefrontUrl?: string
): string | undefined {
  return isErc8004Listing(item) ? SCAN_URL : storefrontUrl;
}
