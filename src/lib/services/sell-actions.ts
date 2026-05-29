export function serviceOfferResourcePath(
  name: string,
  namespace: string
): string {
  return `/api/sell/${encodeURIComponent(name)}?ns=${encodeURIComponent(namespace)}`;
}
