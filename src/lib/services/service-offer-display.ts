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

export function formatUsdcAmount(amount: number): string {
  if (!Number.isFinite(amount) || amount < 0) return '';
  if (amount >= 1) return amount.toFixed(2);
  return amount.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 6,
  });
}

export function serviceOfferUsdcEquivalent(
  price: string,
  assetSymbol: string,
  obolUsdPrice: number | null | undefined
): string | null {
  if (assetSymbol.trim().toUpperCase() !== 'OBOL') return null;
  if (
    obolUsdPrice === null ||
    obolUsdPrice === undefined ||
    !Number.isFinite(obolUsdPrice) ||
    obolUsdPrice <= 0
  ) {
    return null;
  }

  const amount = Number(price);
  if (!Number.isFinite(amount) || amount < 0) return null;

  return formatUsdcAmount(amount * obolUsdPrice);
}
