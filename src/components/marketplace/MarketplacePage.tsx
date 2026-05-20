'use client';

import { useEffect, useMemo, useState } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { LoadingState } from '@/components/molecules/LoadingState';
import { ErrorState } from '@/components/molecules/ErrorState';
import { EmptyMessage } from '@/components/atoms/EmptyMessage';
import { Pagination } from '@/components/atoms/Pagination';
import { useMarketplaceCatalog } from '@/hooks/useMarketplaceCatalog';
import type {
  MarketplaceFilters,
  MarketplaceView,
  ServiceListItem,
} from '@/lib/marketplace/types';

import { ViewToggle, readStoredView } from './ViewToggle';
import { MarketplaceFacets } from './MarketplaceFacets';
import { MarketplaceGrid } from './MarketplaceGrid';
import { MarketplaceTable } from './MarketplaceTable';

const Layout = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'minmax(240px, 280px) 1fr',
  gap: '$lg',
  padding: '$lg',
  maxWidth: 1440,
  margin: '0 auto',

  '@media (max-width: 960px)': {
    gridTemplateColumns: '1fr',
  },
});

const Header = styled(Flex, {
  gridColumn: '1 / -1',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$md',
});

const TitleBlock = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
});

const Title = styled(Text, {
  fontSize: '$6',
  fontWeight: '$semibold',
  color: '$light',
});

const Subtitle = styled(Text, {
  fontSize: '$2',
  color: '$body',
});

const Toolbar = styled(Flex, {
  gridColumn: '1 / -1',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$md',
});

const Results = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const PaginationRow = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$xs',
  paddingTop: '$md',
});

const DEFAULT_FILTERS: MarketplaceFilters = {
  sort: 'recent',
  page: 1,
  pageSize: 24,
};

export interface MarketplacePageProps {
  initialView?: MarketplaceView;
}

export function MarketplacePage({
  initialView = 'cards',
}: MarketplacePageProps) {
  const [view, setView] = useState<MarketplaceView>(initialView);
  const [filters, setFilters] = useState<MarketplaceFilters>(DEFAULT_FILTERS);

  // Hydration safety: localStorage is only available client-side. Read once
  // on mount; the initial render uses initialView to avoid SSR mismatch.
  useEffect(() => {
    setView(readStoredView(initialView));
  }, [initialView]);

  // When the user switches to table view we keep their card-view pageSize but
  // bump it for denser scanning. Cards default to 24, table to 50.
  useEffect(() => {
    setFilters(prev => ({
      ...prev,
      pageSize: view === 'list' ? 50 : 24,
      page: 1,
    }));
  }, [view]);

  const { data, isLoading, isError, error } = useMarketplaceCatalog(filters);

  const totalPages = useMemo(() => {
    if (!data) return 1;
    return Math.max(1, Math.ceil(data.total / (filters.pageSize ?? 24)));
  }, [data, filters.pageSize]);

  const onDetails = (item: ServiceListItem) => {
    // PR-C wires this to /marketplace/[slug]. For now we just log so the
    // wire-up is observable.
    console.info('[marketplace] details', item.slug);
  };
  const onBuy = (item: ServiceListItem) => {
    console.info('[marketplace] buy', item.slug);
  };

  return (
    <Layout>
      <Header>
        <TitleBlock>
          <Title>Marketplace</Title>
          <Subtitle>
            Discover x402-gated services published by Obol Stack operators.
          </Subtitle>
        </TitleBlock>
      </Header>

      <Box>
        <MarketplaceFacets
          filters={filters}
          facets={data?.facets ?? { type: [], network: [], asset: [] }}
          onChange={setFilters}
        />
      </Box>

      <Box>
        <Toolbar css={{ marginBottom: '$md' }}>
          <Subtitle>
            {data ? `${data.total} service${data.total === 1 ? '' : 's'}` : '—'}
          </Subtitle>
          <ViewToggle value={view} onChange={setView} />
        </Toolbar>
        <Results>
          {isLoading ? (
            <LoadingState message="Loading services…" />
          ) : isError ? (
            <ErrorState message={error?.message ?? 'Failed to load catalog'} />
          ) : !data || data.items.length === 0 ? (
            <EmptyMessage>
              No services match your filters. Try clearing the search or
              widening the price range.
            </EmptyMessage>
          ) : view === 'list' ? (
            <MarketplaceTable
              items={data.items}
              sort={filters.sort ?? 'recent'}
              onSortChange={sort =>
                setFilters(prev => ({ ...prev, sort, page: 1 }))
              }
              onDetails={onDetails}
              onBuy={onBuy}
            />
          ) : (
            <MarketplaceGrid
              items={data.items}
              onDetails={onDetails}
              onBuy={onBuy}
            />
          )}
          {data && data.total > 0 ? (
            <PaginationRow>
              <Subtitle>
                Page {filters.page ?? 1} of {totalPages}
              </Subtitle>
              <Pagination
                page={filters.page ?? 1}
                totalPages={totalPages}
                onPrev={() =>
                  setFilters(prev => ({
                    ...prev,
                    page: Math.max(1, (prev.page ?? 1) - 1),
                  }))
                }
                onNext={() =>
                  setFilters(prev => ({
                    ...prev,
                    page: Math.min(totalPages, (prev.page ?? 1) + 1),
                  }))
                }
              />
            </PaginationRow>
          ) : null}
        </Results>
      </Box>
    </Layout>
  );
}
