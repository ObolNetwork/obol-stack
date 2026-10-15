export interface ServiceOfferDisplayPayment {
  asset?: {
    symbol?: string;
  };
}

export interface ServiceOfferDisplayRegistration {
  supportedTrust?: string[];
}

export function serviceOfferAssetSymbol(
  payment: ServiceOfferDisplayPayment | undefined
): string {
  const symbol = payment?.asset?.symbol?.trim();
  return symbol || 'token';
}

export function serviceOfferTrustBadges(
  registration: ServiceOfferDisplayRegistration | undefined
): string[] {
  const seen = new Set<string>();
  const badges: string[] = [];
  for (const method of registration?.supportedTrust ?? []) {
    const trimmed = method.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    badges.push(formatTrustMethod(trimmed));
  }
  return badges;
}

function formatTrustMethod(method: string): string {
  switch (method) {
    case 'crypto-economic':
      return 'Crypto-economic';
    case 'tee-attestation':
      return 'TEE attestation';
    case 'reputation':
      return 'Reputation';
    default:
      return method
        .split('-')
        .filter(Boolean)
        .map(part => part.charAt(0).toUpperCase() + part.slice(1))
        .join(' ');
  }
}
