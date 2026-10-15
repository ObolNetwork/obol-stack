import { notFound } from 'next/navigation';

import { decodeSlugParam, isWellFormedSlug } from '@/lib/marketplace/slug';
import { MarketplaceDetailRoute } from './MarketplaceDetailRoute';

/**
 * Next encodes `/` in slugs, so a "ns/name" pair arrives as "ns%2Fname".
 * Malformed slugs 404 here; well-formed slugs that the catalog doesn't know
 * 404 client-side once /api/marketplace/services/[slug] answers.
 */
export default async function MarketplaceServiceRoute({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const decoded = decodeSlugParam(slug);
  if (!decoded || !isWellFormedSlug(decoded)) notFound();

  return <MarketplaceDetailRoute slug={decoded} />;
}
