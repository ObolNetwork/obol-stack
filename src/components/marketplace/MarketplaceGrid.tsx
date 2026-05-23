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
  gridTemplateColumns: 'repeat(2, minmax(0, 1fr))',

  '@media (max-width: 720px)': {
    gridTemplateColumns: 'minmax(0, 1fr)',
  },
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
