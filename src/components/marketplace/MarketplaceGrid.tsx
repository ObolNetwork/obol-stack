'use client';

import { Box, styled } from '@obolnetwork/obol-ui';

import type { ServiceListItem } from '@/lib/marketplace/types';
import { MarketplaceCard } from './MarketplaceCard';

export interface MarketplaceGridProps {
  items: readonly ServiceListItem[];
  onDetails?: (item: ServiceListItem) => void;
  onBuy?: (item: ServiceListItem) => void;
}

const Grid = styled(Box, {
  display: 'grid',
  gap: '$sm',
  // Match the reference card view: preserve the two-column grid when space
  // allows so a single card does not stretch across the full result area.
  gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 500px), 1fr))',
});

export function MarketplaceGrid({
  items,
  onDetails,
  onBuy,
}: MarketplaceGridProps) {
  return (
    <Grid>
      {items.map(item => (
        <MarketplaceCard
          key={item.slug}
          item={item}
          onDetails={onDetails}
          onBuy={onBuy}
        />
      ))}
    </Grid>
  );
}
