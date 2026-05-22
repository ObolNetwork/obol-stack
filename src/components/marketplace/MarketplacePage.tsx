'use client';

import { useEffect, useMemo, useState } from 'react';
import { useRouter } from 'next/navigation';
import dynamic from 'next/dynamic';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { LoadingState } from '@/components/molecules/LoadingState';
import { ErrorState } from '@/components/molecules/ErrorState';
import { StackUnreachableState } from '@/components/molecules/StackUnreachableState';
import { EmptyMessage } from '@/components/atoms/EmptyMessage';
import { Pagination } from '@/components/atoms/Pagination';
import { useMarketplaceCatalog } from '@/hooks/useMarketplaceCatalog';
import { isStackUnreachable } from '@/lib/marketplace/errors';
import type {
  MarketplaceFilters,
  MarketplaceView,
  ServiceListItem,
} from '@/lib/marketplace/types';

import { ViewToggle, readStoredView } from './ViewToggle';
import { MarketplaceFacets } from './MarketplaceFacets';
import { MarketplaceGrid } from './MarketplaceGrid';
import { MarketplaceTable } from './MarketplaceTable';

import type { BuyDrawerProps } from './BuyDrawer';

// BuyDrawer pulls in the buy-command builders + CopyButton + dialog
// machinery; only relevant after the user clicks Buy. Lazy-load it so
// it doesn't ship in the initial /marketplace route bundle.
// (vercel-react-best-practices: bundle-dynamic-imports)
const BuyDrawer = dynamic<BuyDrawerProps>(
  () => import('./BuyDrawer.tsx').then(m => m.BuyDrawer),
  { ssr: false }
);

// The outer shell (background, page padding, header chrome, footer) is
// supplied by <DashboardTemplate> in src/app/marketplace/layout.tsx. The
// Layout here only owns the facets-rail + content split.
const Layout = styled(Box, {
  display: 'flex',
  gap: '$md',
  width: '100%',

  '@media (max-width: 960px)': {
    flexDirection: 'column',
  },
});

const RightCol = styled(Box, {
  flex: 1,
  minWidth: 0,
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const TitleBlock = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxxs',
});

const Title = styled(Text, {
  fontSize: '$6',
  fontWeight: '$semibold',
  color: '$light',
  letterSpacing: '-0.01em',
});

const Subtitle = styled(Text, {
  fontSize: '$2',
  color: '$body',
});

const ResultsBar = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
});

const ResultsCount = styled(Text, {
  color: '$body',
  fontSize: '$3',
});

const ResultsCountStrong = styled('span', {
  color: '$light',
  fontWeight: '$semibold',
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
  const router = useRouter();
  const [view, setView] = useState<MarketplaceView>(initialView);
  const [filters, setFilters] = useState<MarketplaceFilters>(DEFAULT_FILTERS);
  const [buyTarget, setBuyTarget] = useState<ServiceListItem | null>(null);

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

  const { data, isLoading, isError, error, refetch } =
    useMarketplaceCatalog(filters);

  const totalPages = useMemo(() => {
    if (!data) return 1;
    return Math.max(1, Math.ceil(data.total / (filters.pageSize ?? 24)));
  }, [data, filters.pageSize]);

  const onDetails = (item: ServiceListItem) => {
    router.push(`/marketplace/${encodeURIComponent(item.slug)}`);
  };
  const onBuy = (item: ServiceListItem) => {
    setBuyTarget(item);
  };

  return (
    <Layout>
      <MarketplaceFacets
        filters={filters}
        facets={
          data?.facets ?? { type: [], network: [], asset: [], source: [] }
        }
        onChange={setFilters}
      />

      <RightCol>
        <TitleBlock>
          <Title>Marketplace</Title>
          <Subtitle>
            Discover x402-gated services published by Obol Stack operators.
          </Subtitle>
        </TitleBlock>

        <ResultsBar>
          <ResultsCount>
            <ResultsCountStrong>{data?.total ?? '—'}</ResultsCountStrong>{' '}
            service{data?.total === 1 ? '' : 's'}
          </ResultsCount>
          <Box css={{ flex: 1 }} />
          <ViewToggle value={view} onChange={setView} />
        </ResultsBar>

        <Results>
          {isLoading ? (
            <LoadingState message="Loading services…" />
          ) : isError && isStackUnreachable(error) ? (
            <StackUnreachableState
              detail={error.message}
              onRetry={() => refetch()}
            />
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
      </RightCol>
      <BuyDrawer
        item={buyTarget}
        open={buyTarget !== null}
        onClose={() => setBuyTarget(null)}
      />
    </Layout>
  );
}
