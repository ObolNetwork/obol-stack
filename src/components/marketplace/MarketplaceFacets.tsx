'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { type ChangeEvent } from 'react';
import { CheckIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';

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

const FacetItem = styled('button', {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: '7px 10px',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  cursor: 'pointer',
  textAlign: 'left',
  color: '$body',
  fontSize: '$2',
  fontFamily: '$fonts$body',
  transition: 'background 120ms, border-color 120ms, color 120ms',

  '&:hover': { color: '$light', borderColor: '$bg05' },

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
        '&:hover': { color: '$obolGreen' },
      },
    },
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

// Visual price-range slider — decorative, matches the canvas 1:1.
// Functional drag handles are a follow-up; for now this shows the
// current min/max as percentage thumbs on a track that always spans
// $0 → maximum-seen-price, with the labels reflecting filter state.
const SliderTrack = styled(Box, {
  position: 'relative',
  height: 6,
  background: '$bg03',
  borderRadius: 999,
  marginTop: 6,
  border: '1px solid $stroke',
});

const SliderFill = styled(Box, {
  position: 'absolute',
  top: 0,
  bottom: 0,
  background: '$obolGreen',
  borderRadius: 999,
  opacity: 0.5,
});

const SliderThumb = styled(Box, {
  position: 'absolute',
  top: -5,
  width: 14,
  height: 14,
  borderRadius: 999,
  background: '$obolGreen',
  transform: 'translateX(-50%)',
  border: '2px solid $bg02',
});

const SliderLabels = styled(Flex, {
  justifyContent: 'space-between',
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
  marginTop: 4,
});

// Registry tabs — same two-button "tablist" group the canvas uses,
// rendered at the bottom of the rail.
const TabRow = styled(Box, {
  display: 'grid',
  gridTemplateColumns: '1fr 1fr',
  padding: 3,
  gap: 2,
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
});

const TabButton = styled('button', {
  padding: '6px 8px',
  borderRadius: '$1',
  border: 0,
  cursor: 'pointer',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontWeight: '$medium',
  color: '$body',
  background: 'transparent',
  textAlign: 'center',
  whiteSpace: 'nowrap',
  transition: 'background 120ms, color 120ms',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        fontWeight: '$semibold',
      },
    },
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

  // "All" is implicit when the type filter has no selections — show it
  // as a first row alongside the bucketed facets, per the design.
  const typeAllActive = !filters.type?.length;
  const sourceAllActive = !filters.source?.length;

  // Visual slider geometry. minPrice/maxPrice live in filters as
  // decimal strings; map onto a 0–1 fraction for the thumb positions.
  // If no bound is set, default to the canvas's 8%/68% placeholder so
  // the slider looks alive even with empty filters.
  const minFrac = priceFraction(filters.minPrice, 0.08);
  const maxFrac = priceFraction(filters.maxPrice, 0.68);

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
              {sourceAllActive ? <CheckIcon width={10} height={10} /> : null}
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

      <Group>
        <FieldLabel>Price range ({primaryToken(facets)})</FieldLabel>
        <SliderTrack>
          <SliderFill
            css={{
              left: `${(minFrac * 100).toFixed(0)}%`,
              right: `${((1 - maxFrac) * 100).toFixed(0)}%`,
            }}
          />
          <SliderThumb css={{ left: `${(minFrac * 100).toFixed(0)}%` }} />
          <SliderThumb css={{ left: `${(maxFrac * 100).toFixed(0)}%` }} />
        </SliderTrack>
        <SliderLabels>
          <span>{filters.minPrice ? filters.minPrice : '$0.0001'}</span>
          <span>{filters.maxPrice ? filters.maxPrice : '$2.50'}</span>
        </SliderLabels>
      </Group>

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

function priceFraction(value: string | undefined, fallback: number): number {
  if (!value) return fallback;
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return fallback;
  // Clamp to the same logarithmic range the slider visualizes
  // ($0.0001 .. $2.50). Anything outside snaps to the edge.
  const min = -4; // log10(0.0001)
  const max = Math.log10(2.5);
  const v = Math.log10(n);
  return Math.min(1, Math.max(0, (v - min) / (max - min)));
}

function primaryToken(facets: Facets): string {
  const top = facets.asset[0];
  return top?.value ?? 'USDC';
}
