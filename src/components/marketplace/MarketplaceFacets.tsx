'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { type ChangeEvent } from 'react';
import { CheckIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';

import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import type {
  MarketplaceFacets as Facets,
  MarketplaceFilters,
  MarketplaceRegistry,
  MarketplaceSource,
} from '@/lib/marketplace/types';
import type { ChainName } from '@/types/dashboard';
import type { ServiceOfferType } from '@/types/sell';

export interface MarketplaceFacetsProps {
  filters: MarketplaceFilters;
  facets: Facets;
  onChange: (next: MarketplaceFilters) => void;
}

// 224px rail per Services.html canvas, sections separated by $lg, padded
// on the right so the column doesn't kiss the result grid.
const Panel = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
  width: 224,
  flexShrink: 0,
  paddingRight: '$md',

  '@media (max-width: 960px)': {
    width: '100%',
    paddingRight: 0,
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: '$sm',
  },
});

const SearchWrap = styled(Box, { position: 'relative' });

const SearchIconBox = styled(Flex, {
  position: 'absolute',
  left: 12,
  top: '50%',
  transform: 'translateY(-50%)',
  color: '$body',
  pointerEvents: 'none',
});

const SearchInput = styled('input', {
  width: '100%',
  height: 36,
  paddingLeft: 34,
  paddingRight: '$sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$2',
  fontFamily: '$fonts$body',

  '&::placeholder': { color: '$body' },
  '&:focus': { outline: 'none', borderColor: '$obolGreen' },
});

const Group = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: 6,
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
});

const Items = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: 4,
});

// Facet checkbox row — Button atom (ghost/sm) plus the layout the rail
// needs: full-width, left-aligned content, selected state.
const FacetItem = styled(Button, {
  width: '100%',
  justifyContent: 'flex-start',
  textAlign: 'left',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
        '&:hover:not(:disabled)': {
          color: '$obolGreen',
          borderColor: '$obolGreen',
        },
      },
    },
  },

  defaultVariants: {
    variant: 'ghost',
    size: 'sm',
  },
});

const CheckBox = styled(Flex, {
  width: 12,
  height: 12,
  borderRadius: 3,
  alignItems: 'center',
  justifyContent: 'center',
  border: '1px solid',
  borderColor: '$stroke',
  color: '#062018',
  flexShrink: 0,

  variants: {
    active: {
      true: { background: '$obolGreen', borderColor: '$obolGreen' },
    },
  },
});

const ItemLabel = styled(Text, {
  flex: 1,
  fontSize: '$2',
  color: 'inherit',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const Count = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

// Functional dual-thumb price-range slider — two native range inputs stacked
// over a shared track, log-spaced stops so sub-cent and dollar prices both get
// room. Writes minPrice/maxPrice (decimal strings) which filterItems honours.
const PriceHeader = styled(Flex, {
  alignItems: 'center',
});

const ResetButton = styled('button', {
  background: 'transparent',
  border: 0,
  padding: 0,
  cursor: 'pointer',
  color: '$obolGreen',
  fontSize: '$1',
  fontFamily: '$fonts$body',
});

const RangeWrap = styled(Box, {
  position: 'relative',
  height: 20,
  margin: '2px 8px',
});

const RangeBase = styled(Box, {
  position: 'absolute',
  top: '50%',
  left: 0,
  right: 0,
  height: 4,
  transform: 'translateY(-50%)',
  background: '$bg04',
  borderRadius: 999,
});

const RangeFill = styled(Box, {
  position: 'absolute',
  top: '50%',
  height: 4,
  transform: 'translateY(-50%)',
  background: '$obolGreen',
  borderRadius: 999,
});

const RangeInput = styled('input', {
  WebkitAppearance: 'none',
  appearance: 'none',
  position: 'absolute',
  left: 0,
  right: 0,
  width: '100%',
  height: 20,
  margin: 0,
  background: 'transparent',
  pointerEvents: 'none',

  '&::-webkit-slider-thumb': {
    WebkitAppearance: 'none',
    appearance: 'none',
    width: 15,
    height: 15,
    borderRadius: '50%',
    background: '$obolGreen',
    border: '2px solid $bg02',
    cursor: 'pointer',
    pointerEvents: 'auto',
    boxShadow: '0 1px 2px rgba(0,0,0,0.35)',
  },
  '&::-moz-range-thumb': {
    width: 15,
    height: 15,
    borderRadius: '50%',
    background: '$obolGreen',
    border: '2px solid $bg02',
    cursor: 'pointer',
    pointerEvents: 'auto',
  },
  '&::-webkit-slider-runnable-track': { background: 'transparent' },
  '&::-moz-range-track': { background: 'transparent' },
  '&:focus': { outline: 'none' },
});

const RangeLabels = styled(Flex, {
  justifyContent: 'space-between',
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
  marginTop: 4,
});

// Log-spaced stops: index 0 means "no lower bound", the last index means
// "no upper bound", so a full-width slider clears the price filter.
const PRICE_STOPS = [
  0.0001, 0.0005, 0.001, 0.002, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 2.5,
] as const;
const PRICE_MAX_IDX = PRICE_STOPS.length - 1;

function fmtPrice(v: number): string {
  return v < 0.01 ? v.toFixed(4) : v.toFixed(2);
}

// Snap a decimal-string bound to the nearest slider stop index.
function stopIndex(value: string | undefined, fallback: number): number {
  if (!value) return fallback;
  const n = Number(value);
  if (!Number.isFinite(n)) return fallback;
  let best = fallback;
  let bestDist = Infinity;
  PRICE_STOPS.forEach((s, i) => {
    const d = Math.abs(s - n);
    if (d < bestDist) {
      bestDist = d;
      best = i;
    }
  });
  return best;
}

function PriceRange({
  token,
  minPrice,
  maxPrice,
  onCommit,
}: {
  token: string;
  minPrice?: string;
  maxPrice?: string;
  onCommit: (min: string | undefined, max: string | undefined) => void;
}) {
  const lo = stopIndex(minPrice, 0);
  const hi = stopIndex(maxPrice, PRICE_MAX_IDX);
  const active = !(lo === 0 && hi === PRICE_MAX_IDX);
  const pct = (i: number) => (i / PRICE_MAX_IDX) * 100;
  const commit = (nlo: number, nhi: number) =>
    onCommit(
      nlo === 0 ? undefined : String(PRICE_STOPS[nlo]),
      nhi === PRICE_MAX_IDX ? undefined : String(PRICE_STOPS[nhi])
    );

  return (
    <Group>
      <PriceHeader>
        <FieldLabel>Price range ({token})</FieldLabel>
        <Box css={{ flex: 1 }} />
        {active ? (
          <ResetButton
            type="button"
            onClick={() => onCommit(undefined, undefined)}
          >
            Reset
          </ResetButton>
        ) : null}
      </PriceHeader>
      <RangeWrap>
        <RangeBase />
        <RangeFill css={{ left: `${pct(lo)}%`, right: `${100 - pct(hi)}%` }} />
        <RangeInput
          type="range"
          min={0}
          max={PRICE_MAX_IDX}
          step={1}
          value={lo}
          aria-label="Minimum price"
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            commit(Math.min(Number(e.target.value), hi), hi)
          }
        />
        <RangeInput
          type="range"
          min={0}
          max={PRICE_MAX_IDX}
          step={1}
          value={hi}
          aria-label="Maximum price"
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            commit(lo, Math.max(Number(e.target.value), lo))
          }
        />
      </RangeWrap>
      <RangeLabels>
        <span>{fmtPrice(PRICE_STOPS[lo])}</span>
        <span>{fmtPrice(PRICE_STOPS[hi])}</span>
      </RangeLabels>
    </Group>
  );
}

// Registry tabs — same two-button "tablist" group the canvas uses,
// rendered at the bottom of the rail.
const TabRow = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr 1fr',
  padding: 3,
  gap: 2,
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$pill',
});

// Segmented-control tab — Button atom (bare/sm/pill) with a selected state.
const TabButton = styled(Button, {
  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        '&:hover:not(:disabled)': { color: '$obolGreen' },
      },
    },
  },

  defaultVariants: {
    variant: 'bare',
    size: 'sm',
    pill: true,
  },
});

const REGISTRY_TABS: ReadonlyArray<{
  value: MarketplaceRegistry;
  label: string;
}> = [
  { value: 'obol', label: 'ObolStack' },
  { value: 'erc8004', label: 'Public ERC-8004' },
];

const TYPE_LABEL: Record<ServiceOfferType, string> = {
  inference: 'Inference',
  agent: 'Agent',
  http: 'HTTP',
  'fine-tuning': 'Fine-tune',
};

const SOURCE_LABEL: Record<MarketplaceSource, string> = {
  local: 'Local cluster',
  remote: 'Remote agents',
};

const CAIP2_TO_CHAIN_NAME: Record<string, ChainName> = {
  'eip155:1': 'mainnet',
  'eip155:11155111': 'sepolia',
  'eip155:8453': 'base',
  'eip155:84532': 'base-sepolia',
};

const NETWORK_LABEL: Record<string, string> = {
  'eip155:1': 'Ethereum',
  'eip155:11155111': 'Sepolia',
  'eip155:8453': 'Base',
  'eip155:84532': 'Base Sepolia',
};

function toggle<T>(arr: T[] | undefined, value: T): T[] {
  const set = new Set(arr ?? []);
  if (set.has(value)) set.delete(value);
  else set.add(value);
  return Array.from(set);
}

export function MarketplaceFacets({
  filters,
  facets,
  onChange,
}: MarketplaceFacetsProps) {
  const patch = (next: Partial<MarketplaceFilters>) =>
    onChange({ ...filters, ...next, page: 1 });

  const activeRegistry: MarketplaceRegistry = filters.registry ?? 'obol';
  // For ERC-8004 we only have real totals for chains; type/token/source counts
  // are page-level and misleading, so we hide them.
  const isErc8004 = activeRegistry === 'erc8004';

  // "All" is implicit when the type filter has no selections — show it
  // as a first row alongside the bucketed facets, per the design.
  const typeAllActive = !filters.type?.length;
  const sourceAllActive = !filters.source?.length;

  return (
    <Panel as="aside">
      <SearchWrap>
        <SearchIconBox>
          <MagnifyingGlassIcon width={14} height={14} />
        </SearchIconBox>
        <SearchInput
          placeholder="Search services…"
          value={filters.q ?? ''}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            patch({ q: e.target.value || undefined })
          }
        />
      </SearchWrap>

      {!isErc8004 && (
        <Group>
          <FieldLabel>Type</FieldLabel>
          <Items>
            <FacetItem
              type="button"
              active={typeAllActive}
              onClick={() => patch({ type: undefined })}
            >
              <CheckBox active={typeAllActive}>
                {typeAllActive ? <CheckIcon width={10} height={10} /> : null}
              </CheckBox>
              <ItemLabel>All</ItemLabel>
              <Count>{facets.type.reduce((n, b) => n + b.count, 0)}</Count>
            </FacetItem>
            {facets.type.map(bucket => {
              const value = bucket.value as ServiceOfferType;
              const active = filters.type?.includes(value) ?? false;
              return (
                <FacetItem
                  key={value}
                  type="button"
                  active={active}
                  onClick={() =>
                    patch({
                      type: toggle(filters.type, value) as ServiceOfferType[],
                    })
                  }
                >
                  <CheckBox active={active}>
                    {active ? <CheckIcon width={10} height={10} /> : null}
                  </CheckBox>
                  <ItemLabel>{TYPE_LABEL[value] ?? value}</ItemLabel>
                  <Count>{bucket.count}</Count>
                </FacetItem>
              );
            })}
          </Items>
        </Group>
      )}

      <Group>
        <FieldLabel>Chain</FieldLabel>
        <Items>
          {facets.network.map(bucket => {
            const active = filters.network?.includes(bucket.value) ?? false;
            const chainName = CAIP2_TO_CHAIN_NAME[bucket.value];
            return (
              <FacetItem
                key={bucket.value}
                type="button"
                active={active}
                onClick={() =>
                  patch({ network: toggle(filters.network, bucket.value) })
                }
              >
                <CheckBox active={active}>
                  {active ? <CheckIcon width={10} height={10} /> : null}
                </CheckBox>
                {chainName ? <ChainIcon chain={chainName} size={14} /> : null}
                <ItemLabel>
                  {NETWORK_LABEL[bucket.value] ?? bucket.value}
                </ItemLabel>
                <Count>{bucket.count}</Count>
              </FacetItem>
            );
          })}
        </Items>
      </Group>

      {!isErc8004 && (
        <>
          <Group>
            <FieldLabel>Token</FieldLabel>
            <Items>
              {facets.asset.map(bucket => {
                const active = filters.asset?.includes(bucket.value) ?? false;
                return (
                  <FacetItem
                    key={bucket.value}
                    type="button"
                    active={active}
                    onClick={() =>
                      patch({ asset: toggle(filters.asset, bucket.value) })
                    }
                  >
                    <CheckBox active={active}>
                      {active ? <CheckIcon width={10} height={10} /> : null}
                    </CheckBox>
                    <ItemLabel>{bucket.value}</ItemLabel>
                    <Count>{bucket.count}</Count>
                  </FacetItem>
                );
              })}
            </Items>
          </Group>

          <Group>
            <FieldLabel>Source</FieldLabel>
            <Items>
              <FacetItem
                type="button"
                active={sourceAllActive}
                onClick={() => patch({ source: undefined })}
              >
                <CheckBox active={sourceAllActive}>
                  {sourceAllActive ? (
                    <CheckIcon width={10} height={10} />
                  ) : null}
                </CheckBox>
                <ItemLabel>All</ItemLabel>
                <Count>{facets.source.reduce((n, b) => n + b.count, 0)}</Count>
              </FacetItem>
              {(['local', 'remote'] as const).map(value => {
                const bucket = facets.source.find(b => b.value === value);
                const active = filters.source?.includes(value) ?? false;
                return (
                  <FacetItem
                    key={value}
                    type="button"
                    active={active}
                    onClick={() =>
                      patch({
                        source: toggle(
                          filters.source,
                          value
                        ) as MarketplaceSource[],
                      })
                    }
                  >
                    <CheckBox active={active}>
                      {active ? <CheckIcon width={10} height={10} /> : null}
                    </CheckBox>
                    <ItemLabel>{SOURCE_LABEL[value]}</ItemLabel>
                    <Count>{bucket?.count ?? 0}</Count>
                  </FacetItem>
                );
              })}
            </Items>
          </Group>

          <PriceRange
            token={primaryToken(facets)}
            minPrice={filters.minPrice}
            maxPrice={filters.maxPrice}
            onCommit={(minPrice, maxPrice) => patch({ minPrice, maxPrice })}
          />
        </>
      )}

      <Group>
        <FieldLabel>Registry</FieldLabel>
        <TabRow role="tablist" aria-label="Registry">
          {REGISTRY_TABS.map(tab => {
            const active = activeRegistry === tab.value;
            return (
              <TabButton
                key={tab.value}
                role="tab"
                type="button"
                aria-selected={active}
                active={active}
                onClick={() => patch({ registry: tab.value })}
              >
                {tab.label}
              </TabButton>
            );
          })}
        </TabRow>
      </Group>
    </Panel>
  );
}

function primaryToken(facets: Facets): string {
  const top = facets.asset[0];
  return top?.value ?? 'USDC';
}
