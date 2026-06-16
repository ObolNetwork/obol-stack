'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';

import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import type { MarketplaceSort, ServiceListItem } from '@/lib/marketplace/types';

export interface MarketplaceTableProps {
  items: readonly ServiceListItem[];
  sort: MarketplaceSort;
  onSortChange: (sort: MarketplaceSort) => void;
  onDetails?: (item: ServiceListItem) => void;
  onBuy?: (item: ServiceListItem) => void;
}

const Wrapper = styled(Box, {
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  overflow: 'hidden',
});

const Table = styled('table', {
  width: '100%',
  borderCollapse: 'collapse',
  fontFamily: '$fonts$body',
  fontSize: '$1',
});

const Thead = styled('thead', {
  background: '$bg03',
});

const Th = styled('th', {
  textAlign: 'left',
  padding: '$xs $md',
  fontSize: '$0',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
  fontWeight: '$semibold',
  borderBottom: '1px solid $stroke',
  whiteSpace: 'nowrap',
});

const SortableTh = styled(Th, {
  cursor: 'pointer',
  userSelect: 'none',
  '&:hover': { color: '$light' },
});

const Td = styled('td', {
  padding: '$xs $md',
  color: '$light',
  borderBottom: '1px solid $stroke',
  verticalAlign: 'middle',
});

const Row = styled('tr', {
  transition: 'background 0.1s ease',
  '&:last-child td': { borderBottom: 'none' },
  '&:hover': { background: '$bg03' },
});

const Mono = styled(Text, {
  fontFamily: 'monospace',
  fontSize: '$1',
  color: '$body',
});

const TypePill = styled(Text, {
  display: 'inline-block',
  padding: '2px 8px',
  borderRadius: '$pill',
  background: '$bg03',
  border: '1px solid $stroke',
  color: '$body',
  fontSize: '$0',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const Actions = styled(Flex, {
  gap: '$xs',
  justifyContent: 'flex-end',
});

const PrimaryBtn = styled('button', {
  height: 26,
  padding: '0 $xs',
  borderRadius: '$1',
  border: 'none',
  background: '$obolGreen',
  color: '$obolDarkDark',
  fontSize: '$0',
  fontWeight: '$semibold',
  cursor: 'pointer',
  '&:hover': { background: '$obolGreenHover' },
});

const GhostBtn = styled('button', {
  height: 26,
  padding: '0 $xs',
  borderRadius: '$1',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$light',
  fontSize: '$0',
  cursor: 'pointer',
  '&:hover': { borderColor: '$body' },
});

const SortIndicator = styled(Text, {
  display: 'inline-block',
  marginLeft: '$xxs',
  color: '$obolGreen',
});

function statusLabel(status: ServiceListItem['status']): string {
  switch (status) {
    case 'live':
      return '● live';
    case 'pending-registration':
      return '◐ pending';
    case 'degraded':
      return '◯ degraded';
  }
}

function networkLabel(item: ServiceListItem): string {
  return item.network.alias || item.network.caip2;
}

function nextSort(
  current: MarketplaceSort,
  column: 'price' | 'name'
): MarketplaceSort {
  if (column === 'price') {
    return current === 'price_asc' ? 'price_desc' : 'price_asc';
  }
  return 'name';
}

export function MarketplaceTable({
  items,
  sort,
  onSortChange,
  onDetails,
  onBuy,
}: MarketplaceTableProps) {
  return (
    <Wrapper>
      <Table>
        <Thead>
          <tr>
            <SortableTh onClick={() => onSortChange(nextSort(sort, 'name'))}>
              Name{sort === 'name' ? <SortIndicator>↑</SortIndicator> : null}
            </SortableTh>
            <Th>Type</Th>
            <Th>Model / Runtime</Th>
            <Th>Network</Th>
            <SortableTh onClick={() => onSortChange(nextSort(sort, 'price'))}>
              Price
              {sort === 'price_asc' ? (
                <SortIndicator>↑</SortIndicator>
              ) : sort === 'price_desc' ? (
                <SortIndicator>↓</SortIndicator>
              ) : null}
            </SortableTh>
            <Th>Asset</Th>
            <Th>Status</Th>
            <Th css={{ textAlign: 'right' }}>Actions</Th>
          </tr>
        </Thead>
        <tbody>
          {items.map(item => (
            <Row key={item.slug}>
              <Td>
                <Text css={{ color: '$light', fontWeight: '$semibold' }}>
                  {item.name}
                </Text>
                <Mono css={{ display: 'block' }}>{item.namespace}</Mono>
              </Td>
              <Td>
                <TypePill>{item.type}</TypePill>
              </Td>
              <Td>
                <Mono>{item.modelOrRuntime || '—'}</Mono>
              </Td>
              <Td>
                <Mono>{networkLabel(item)}</Mono>
              </Td>
              <Td>
                <ObolAmountSwitch
                  amount={item.price || '—'}
                  symbol={item.asset.symbol}
                  unit={`/ ${item.priceUnit}`}
                  showSymbol={false}
                />
              </Td>
              <Td>
                <Mono>{item.asset.symbol}</Mono>
              </Td>
              <Td>{statusLabel(item.status)}</Td>
              <Td>
                <Actions>
                  <GhostBtn type="button" onClick={() => onDetails?.(item)}>
                    Details →
                  </GhostBtn>
                  {item.type === 'inference' ? (
                    <PrimaryBtn type="button" onClick={() => onBuy?.(item)}>
                      Buy
                    </PrimaryBtn>
                  ) : null}
                </Actions>
              </Td>
            </Row>
          ))}
        </tbody>
      </Table>
    </Wrapper>
  );
}
