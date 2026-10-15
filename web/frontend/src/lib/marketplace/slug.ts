/**
 * Marketplace detail slugs are either a cluster listing (`<namespace>/<name>`,
 * both DNS-1123 labels) or an ERC-8004 registry row
 * (`erc8004/<chainId>/<tokenId>`). Anything else can never resolve, so the
 * detail route 404s without a round trip to the catalog API.
 */
const LABEL = '[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?';
const CLUSTER_SLUG = new RegExp(`^${LABEL}/${LABEL}$`);
const ERC8004_SLUG = /^erc8004\/\d{1,12}\/\d{1,78}$/;

export function isWellFormedSlug(slug: string): boolean {
  return CLUSTER_SLUG.test(slug) || ERC8004_SLUG.test(slug);
}

/** Route params may arrive with `/` still percent-encoded (`ns%2Fname`). */
export function decodeSlugParam(raw: string): string | null {
  try {
    return raw.includes('%') ? decodeURIComponent(raw) : raw;
  } catch {
    return null;
  }
}
