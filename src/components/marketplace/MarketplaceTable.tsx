'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ExternalLinkIcon } from '@radix-ui/react-icons';
import { type KeyboardEvent, type MouseEvent } from 'react';

import { Badge } from '@/components/atoms/Badge';
import { Button } from '@/components/atoms/Button';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import { tones } from '@/lib/ui/tones';
import type { MarketplaceSort, ServiceListItem } from '@/lib/marketplace/types';

import { Identicon } from './Identicon';
import {
  BADGE_LABEL,
  BADGE_VARIANT,
  STOREFRONT_TEXT_CSS,
  chainLabel,
  chainName,
  statusLabel,
  statusTone,
  storefrontHref,
} from './service-display';

export interface MarketplaceTableProps {
  items: readonly ServiceListItem[];
  sort: MarketplaceSort;
  onSortChange: (sort: MarketplaceSort) => void;
  onDetails?: (item: ServiceListItem) => void;
  storefrontUrl?: string;
}

const Wrapper = styled(Box, {
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  overflow: 'hidden',
});

const Table = styled('table', {
  width: '100%',
  tableLayout: 'fixed',
  borderCollapse: 'collapse',
  fontFamily: '$fonts$body',
  fontSize: '$2',
});

const Thead = styled('thead', {
  background: '$bg03',
});

const Th = styled('th', {
  textAlign: 'left',
  padding: '10px $md',
  fontSize: '$0',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
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
  padding: '$sm $md',
  color: '$light',
  borderBottom: '1px solid $stroke',
  verticalAlign: 'middle',
});

// Rows are clickable through to the detail page; the whole surface reacts on
// hover so the affordance is obvious. Zebra tint keeps long lists scannable.
const Row = styled('tr', {
  cursor: 'pointer',
  transition: 'background 120ms ease',
  '&:nth-of-type(even)': { background: 'rgba(255,255,255,0.012)' },
  '&:last-child td': { borderBottom: 'none' },
  '&:hover': { background: '$bg03' },
  '&:focus-visible': {
    outline: '2px solid $obolGreen',
    outlineOffset: -2,
  },
});

const NameCell = styled(Flex, {
  alignItems: 'center',
  gap: 10,
  minWidth: 0,
});

const NameText = styled(Text, {
  color: '$light',
  fontWeight: '$semibold',
  fontFamily: 'monospace',
  letterSpacing: 0,
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const Namespace = styled(Text, {
  display: 'block',
  fontFamily: 'monospace',
  fontSize: '$0',
  color: '$muted',
  whiteSpace: 'nowrap',
  overflow: 'hidden',
  textOverflow: 'ellipsis',
});

const Mono = styled(Text, {
  fontFamily: 'monospace',
  fontSize: '$1',
  color: '$body',
});

const ChainCell = styled(Flex, {
  alignItems: 'center',
  gap: 6,
  color: '$light',
  fontSize: '$1',
  whiteSpace: 'nowrap',
});

const StatusCell = styled(Flex, {
  alignItems: 'center',
  gap: 6,
});

const StatusDot = styled('span', {
  width: 8,
  height: 8,
  borderRadius: 999,
  flexShrink: 0,

  variants: {
    tone: {
      live: { background: tones.success.text },
      pending: { background: tones.warning.text },
      degraded: { background: tones.danger.text },
    },
  },
});

const StatusText = styled(Text, {
  fontSize: '$1',
  color: '$body',
  whiteSpace: 'nowrap',
});

const Actions = styled(Flex, {
  gap: '$xs',
  justifyContent: 'flex-end',
});

const SortIndicator = styled(Text, {
  display: 'inline-block',
  marginLeft: '$xxs',
  color: '$obolGreen',
});

const UNIT_SHORT: Record<ServiceListItem['priceUnit'], string> = {
  request: '/ req',
  'agent-turn': '/ turn',
  mtok: '/ Mtok',
  hour: '/ hr',
  epoch: '/ epoch',
};

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
  storefrontUrl,
}: MarketplaceTableProps) {
  return (
    <Wrapper>
      <Table>
        <colgroup>
          <col />
          <col style={{ width: 88 }} />
          <col style={{ width: 158 }} />
          <col style={{ width: 130 }} />
          <col style={{ width: 128 }} />
          <col style={{ width: 100 }} />
          <col style={{ width: 150 }} />
        </colgroup>
        <Thead>
          <tr>
            <SortableTh onClick={() => onSortChange(nextSort(sort, 'name'))}>
              Service
              {sort === 'name' ? <SortIndicator>↑</SortIndicator> : null}
            </SortableTh>
            <Th>Type</Th>
            <Th>Model / Runtime</Th>
            <Th>Network</Th>
            <SortableTh
              css={{ textAlign: 'right' }}
              onClick={() => onSortChange(nextSort(sort, 'price'))}
            >
              Price
              {sort === 'price_asc' ? (
                <SortIndicator>↑</SortIndicator>
              ) : sort === 'price_desc' ? (
                <SortIndicator>↓</SortIndicator>
              ) : null}
            </SortableTh>
            <Th>Status</Th>
            <Th css={{ textAlign: 'right' }}>Actions</Th>
          </tr>
        </Thead>
        <tbody>
          {items.map(item => {
            const tone = statusTone(item.status);
            const chain = chainName(item);
            const seed = item.payTo || item.bannerSeed || item.slug;
            const storeHref = storefrontHref(item, storefrontUrl);
            const openDetails = () => onDetails?.(item);
            return (
              <Row
                key={item.slug}
                role="button"
                tabIndex={0}
                onClick={openDetails}
                onKeyDown={(e: KeyboardEvent<HTMLTableRowElement>) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    openDetails();
                  }
                }}
              >
                <Td>
                  <NameCell>
                    <Identicon seed={seed} size={28} rounded={6} />
                    <Box css={{ minWidth: 0 }}>
                      <NameText title={item.name}>{item.name}</NameText>
                      <Namespace>{item.namespace}</Namespace>
                    </Box>
                  </NameCell>
                </Td>
                <Td>
                  <Badge variant={BADGE_VARIANT[item.type]}>
                    {BADGE_LABEL[item.type]}
                  </Badge>
                </Td>
                <Td>
                  <Mono
                    css={{
                      display: 'block',
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                    title={item.modelOrRuntime || undefined}
                  >
                    {item.modelOrRuntime || '—'}
                  </Mono>
                </Td>
                <Td>
                  <ChainCell>
                    {chain ? <ChainIcon chain={chain} size={16} /> : null}
                    {chainLabel(item)}
                  </ChainCell>
                </Td>
                <Td css={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                  <ObolAmountSwitch
                    amount={item.price || '—'}
                    symbol={item.asset.symbol}
                    unit={item.price ? UNIT_SHORT[item.priceUnit] : undefined}
                    align="right"
                  />
                </Td>
                <Td>
                  <StatusCell>
                    <StatusDot tone={tone} />
                    <StatusText>{statusLabel(item.status)}</StatusText>
                  </StatusCell>
                </Td>
                <Td>
                  <Actions>
                    {storeHref ? (
                      <Button
                        as="a"
                        variant="primary"
                        size="sm"
                        css={STOREFRONT_TEXT_CSS}
                        href={storeHref}
                        target="_blank"
                        rel="noopener noreferrer"
                        onClick={(e: MouseEvent<HTMLAnchorElement>) =>
                          e.stopPropagation()
                        }
                      >
                        Storefront <ExternalLinkIcon width={12} height={12} />
                      </Button>
                    ) : null}
                  </Actions>
                </Td>
              </Row>
            );
          })}
        </tbody>
      </Table>
    </Wrapper>
  );
}
