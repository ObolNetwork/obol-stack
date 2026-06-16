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
import { CopyButton } from '@/components/atoms/CopyButton';
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
import { SortDropdown } from './SortDropdown';
import { ServicesSubNav } from './ServicesSubNav';
import { NewListingDialog } from './NewListingDialog';
import { ExternalInferenceBuy } from './ExternalInferenceBuy';

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

const Subtitle = styled(Text, {
  fontSize: '$2',
  color: '$body',
});

const ResultsBar = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
});

const ResultsCount = styled('span', {
  color: '$body',
  fontSize: '$3',
  lineHeight: 1,
  whiteSpace: 'nowrap',
});

const ResultsCountStrong = styled('span', {
  color: '$light',
  fontWeight: '$semibold',
});

const SourceBreakdown = styled('span', {
  marginLeft: '$xxs',
  color: '$muted',
});

const SortLabel = styled('span', {
  color: '$body',
  fontSize: '$2',
  lineHeight: 1,
  whiteSpace: 'nowrap',
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

const EmptyPanel = styled(Box, {
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  padding: '$lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
  alignItems: 'flex-start',
});

const EmptyTitle = styled(Text, {
  color: '$light',
  fontSize: '$5',
  fontWeight: '$semibold',
});

const EmptyBody = styled(Text, {
  color: '$body',
  fontSize: '$2',
  lineHeight: 1.5,
  maxWidth: 560,
});

const CommandRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  padding: '$xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  maxWidth: '100%',
});

const CommandText = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
  fontSize: '$2',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
});

const LAUNCH_DEMO_COMMAND =
  'obol sell demo quant --pay-to $(obol agent wallet address obol-agent)';

const DEFAULT_FILTERS: MarketplaceFilters = {
  registry: 'obol',
  type: ['inference'],
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
  const [dialogOpen, setDialogOpen] = useState(false);

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

  const sourceBreakdown = useMemo(() => {
    const buckets = data?.facets?.source ?? [];
    if (buckets.length === 0) return '';
    const local = buckets.find(b => b.value === 'local')?.count ?? 0;
    const remote = buckets.find(b => b.value === 'remote')?.count ?? 0;
    const parts: string[] = [];
    if (local > 0) parts.push(`${local} local`);
    if (remote > 0) parts.push(`${remote} remote`);
    return parts.join(' · ');
  }, [data?.facets?.source]);

  const totalPages = useMemo(() => {
    if (!data) return 1;
    return Math.max(1, Math.ceil(data.total / (filters.pageSize ?? 24)));
  }, [data, filters.pageSize]);

  const hasActiveFilters = useMemo(() => {
    return (
      Boolean(filters.registry !== undefined && filters.registry !== 'obol') ||
      Boolean(
        filters.source?.length ||
        filters.type?.length ||
        filters.network?.length ||
        filters.asset?.length ||
        filters.minPrice ||
        filters.maxPrice ||
        filters.q
      )
    );
  }, [filters]);

  const onDetails = (item: ServiceListItem) => {
    router.push(`/marketplace/${encodeURIComponent(item.slug)}`);
  };
  const onBuy = (item: ServiceListItem) => {
    if (item.type !== 'inference') return;
    setBuyTarget(item);
  };

  return (
    <Box css={{ display: 'flex', flexDirection: 'column' }}>
      <ServicesSubNav
        active="browse"
        counts={{ browse: data?.total ?? null }}
        onNewListing={() => setDialogOpen(true)}
      />
      <Layout>
        <MarketplaceFacets
          filters={filters}
          facets={
            data?.facets ?? { type: [], network: [], asset: [], source: [] }
          }
          onChange={setFilters}
        />

        <RightCol>
          <ExternalInferenceBuy />
          <ResultsBar>
            <ResultsCount>
              <ResultsCountStrong>{data?.total ?? '—'}</ResultsCountStrong>{' '}
              service{data?.total === 1 ? '' : 's'}
              {sourceBreakdown ? (
                <SourceBreakdown>· {sourceBreakdown}</SourceBreakdown>
              ) : null}
            </ResultsCount>
            <Box css={{ flex: 1 }} />
            <SortLabel>Sort</SortLabel>
            <SortDropdown
              value={filters.sort ?? 'recent'}
              onChange={sort =>
                setFilters(prev => ({ ...prev, sort, page: 1 }))
              }
            />
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
              <ErrorState
                message={error?.message ?? 'Failed to load catalog'}
              />
            ) : !data || (data.items.length === 0 && hasActiveFilters) ? (
              <EmptyMessage>
                No services match your filters. Try clearing the search or
                widening the price range.
              </EmptyMessage>
            ) : data.items.length === 0 ? (
              <EmptyPanel>
                <EmptyTitle>No services listed yet</EmptyTitle>
                <EmptyBody>
                  Launch the demo quant agent to create the first x402-gated
                  ServiceOffer, or publish your own endpoint with obol sell.
                </EmptyBody>
                <CommandRow>
                  <CommandText title={LAUNCH_DEMO_COMMAND}>
                    {LAUNCH_DEMO_COMMAND}
                  </CommandText>
                  <CopyButton text={LAUNCH_DEMO_COMMAND} title="Copy command" />
                </CommandRow>
              </EmptyPanel>
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
      </Layout>
      <BuyDrawer
        item={buyTarget}
        open={buyTarget !== null}
        onClose={() => setBuyTarget(null)}
      />
      <NewListingDialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
      />
    </Box>
  );
}
