'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { type ChangeEvent } from 'react';
import { CheckIcon, MagnifyingGlassIcon } from '@radix-ui/react-icons';

import type {
  MarketplaceFacets as Facets,
  MarketplaceFilters,
  MarketplaceRegistry,
} from '@/lib/marketplace/types';
import type { ServiceOfferType } from '@/types/sell';

export interface MarketplaceFacetsProps {
  filters: MarketplaceFilters;
  facets: Facets;
  onChange: (next: MarketplaceFilters) => void;
}

const Panel = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
  width: 224,
  flexShrink: 0,
});

const SearchWrap = styled(Box, {
  position: 'relative',
});

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
  gap: '$xs',
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

  '&:hover': {
    color: '$light',
    borderColor: '$bg05',
  },

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
      true: {
        background: '$obolGreen',
        borderColor: '$obolGreen',
      },
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

const PriceRow = styled(Flex, {
  gap: '$xs',
  alignItems: 'center',
});

const PriceInput = styled('input', {
  flex: 1,
  minWidth: 0,
  height: 28,
  padding: '0 $xs',
  borderRadius: '$1',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$1',
  fontFamily: 'monospace',

  '&:focus': { outline: 'none', borderColor: '$obolGreen' },
  '&::-webkit-outer-spin-button, &::-webkit-inner-spin-button': {
    appearance: 'none',
    margin: 0,
  },
});

const Dash = styled(Text, {
  color: '$body',
  fontSize: '$1',
});

// Registry tab group — mirrors the design canvas Services.html.
// Sits at the top of the facets panel and flips the data source between
// the in-cluster Obol catalog and the on-chain ERC-8004 registry.
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

function toggle<T>(arr: T[] | undefined, value: T): T[] {
  const set = new Set(arr ?? []);
  if (set.has(value)) set.delete(value);
  else set.add(value);
  return Array.from(set);
}

function networkLabel(caip2: string): string {
  switch (caip2) {
    case 'eip155:1':
      return 'Ethereum';
    case 'eip155:8453':
      return 'Base';
    case 'eip155:84532':
      return 'Base Sepolia';
    default:
      return caip2;
  }
}

export function MarketplaceFacets({
  filters,
  facets,
  onChange,
}: MarketplaceFacetsProps) {
  const patch = (next: Partial<MarketplaceFilters>) =>
    onChange({ ...filters, ...next, page: 1 });

  const activeRegistry: MarketplaceRegistry = filters.registry ?? 'obol';

  return (
    <Panel>
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
          {facets.type.length === 0 ? (
            <Text css={{ fontSize: '$1', color: '$muted' }}>
              No services indexed.
            </Text>
          ) : (
            facets.type.map(bucket => {
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
            })
          )}
        </Items>
      </Group>

      <Group>
        <FieldLabel>Network</FieldLabel>
        <Items>
          {facets.network.map(bucket => {
            const active = filters.network?.includes(bucket.value) ?? false;
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
                <ItemLabel>{networkLabel(bucket.value)}</ItemLabel>
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
        <FieldLabel>Price range</FieldLabel>
        <PriceRow>
          <PriceInput
            inputMode="decimal"
            placeholder="min"
            value={filters.minPrice ?? ''}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              patch({ minPrice: e.target.value || undefined })
            }
          />
          <Dash>–</Dash>
          <PriceInput
            inputMode="decimal"
            placeholder="max"
            value={filters.maxPrice ?? ''}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              patch({ maxPrice: e.target.value || undefined })
            }
          />
        </PriceRow>
      </Group>
    </Panel>
  );
}
