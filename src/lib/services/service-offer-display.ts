export interface ServiceOfferDisplayPayment {
  asset?: {
    symbol?: string;
  };
}

export function serviceOfferAssetSymbol(
  payment: ServiceOfferDisplayPayment | undefined
): string {
  const symbol = payment?.asset?.symbol?.trim();
  return symbol || 'token';
}
