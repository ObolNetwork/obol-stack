'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useId, type ChangeEvent } from 'react';

import { SearchInput } from '@/components/atoms/SearchInput';
import { FilterChip } from '@/components/atoms/FilterChip';
import type {
  MarketplaceFacets as Facets,
  MarketplaceFilters,
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
  gap: '$md',
  padding: '$md',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
});

const Group = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const GroupTitle = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
});

const ChipRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: '$xxs',
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
  fontFamily: '$fonts$body',

  '&:focus': {
    outline: 'none',
    borderColor: '$obolGreen',
  },

  '&::-webkit-outer-spin-button, &::-webkit-inner-spin-button': {
    appearance: 'none',
    margin: 0,
  },
});

const Dash = styled(Text, {
  color: '$body',
  fontSize: '$1',
});

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
  const minId = useId();
  const maxId = useId();

  const patch = (next: Partial<MarketplaceFilters>) =>
    onChange({ ...filters, ...next, page: 1 });

  return (
    <Panel>
      <Group>
        <GroupTitle>Search</GroupTitle>
        <SearchInput
          placeholder="Name, model, skill…"
          value={filters.q ?? ''}
          onChange={e => patch({ q: e.target.value || undefined })}
        />
      </Group>

      <Group>
        <GroupTitle>Type</GroupTitle>
        <ChipRow>
          {facets.type.length === 0 ? (
            <Dash>No services indexed yet.</Dash>
          ) : (
            facets.type.map(bucket => {
              const value = bucket.value as ServiceOfferType;
              const active = filters.type?.includes(value) ?? false;
              return (
                <FilterChip
                  key={value}
                  active={active}
                  onClick={() =>
                    patch({
                      type: toggle(filters.type, value) as ServiceOfferType[],
                    })
                  }
                >
                  {TYPE_LABEL[value] ?? value} · {bucket.count}
                </FilterChip>
              );
            })
          )}
        </ChipRow>
      </Group>

      <Group>
        <GroupTitle>Network</GroupTitle>
        <ChipRow>
          {facets.network.map(bucket => {
            const active = filters.network?.includes(bucket.value) ?? false;
            return (
              <FilterChip
                key={bucket.value}
                active={active}
                onClick={() =>
                  patch({ network: toggle(filters.network, bucket.value) })
                }
              >
                {networkLabel(bucket.value)} · {bucket.count}
              </FilterChip>
            );
          })}
        </ChipRow>
      </Group>

      <Group>
        <GroupTitle>Asset</GroupTitle>
        <ChipRow>
          {facets.asset.map(bucket => {
            const active = filters.asset?.includes(bucket.value) ?? false;
            return (
              <FilterChip
                key={bucket.value}
                active={active}
                onClick={() =>
                  patch({ asset: toggle(filters.asset, bucket.value) })
                }
              >
                {bucket.value} · {bucket.count}
              </FilterChip>
            );
          })}
        </ChipRow>
      </Group>

      <Group>
        <GroupTitle>Price</GroupTitle>
        <PriceRow>
          <PriceInput
            id={minId}
            inputMode="decimal"
            placeholder="min"
            value={filters.minPrice ?? ''}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              patch({ minPrice: e.target.value || undefined })
            }
          />
          <Dash>–</Dash>
          <PriceInput
            id={maxId}
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
