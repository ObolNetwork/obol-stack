'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Button } from '../atoms/Button';
import { Card } from '../atoms/Card';
import { SectionTitle } from '../atoms/SectionTitle';
import { CountBadge } from '../atoms/CountBadge';
import { Badge } from '../atoms/Badge';
import { StatusPill } from '../atoms/StatusIndicator';
import { Avatar } from '../atoms/Avatar';
import { useServiceOffers } from '../../hooks/useServiceOffers';
import { formatRelativeAgo } from '@/lib/services/service-usage';
import type { ServiceOffer } from '@/types/sell';

function deriveStatus(
  offer: ServiceOffer
): 'synced' | 'syncing' | 'error' | 'unsynced' {
  if (offer.paused) return 'unsynced';
  if (offer.drainEndsAt) return 'syncing';
  const ready = offer.conditions?.find(c => c.type === 'Ready');
  if (ready?.status === 'True') return 'synced';
  if (ready?.status === 'False') return 'error';
  return 'syncing';
}

function deriveStatusLabel(offer: ServiceOffer): string {
  if (offer.paused) return 'Paused';
  if (offer.drainEndsAt) return 'Draining';
  const ready = offer.conditions?.find(c => c.type === 'Ready');
  if (ready?.status === 'True') return 'Live';
  if (ready?.status === 'False') return 'Error';
  return 'Pending';
}

function typeLabel(type: string): string {
  if (type === 'http') return 'HTTP';
  if (type === 'inference') return 'Inference';
  if (type === 'agent') return 'Agent';
  return type;
}

const ListingRow = styled(Box, {
  display: 'grid',
  gridTemplateColumns: 'minmax(0, 1.6fr) auto minmax(0, 0.9fr)',
  gap: '$sm',
  alignItems: 'center',
  padding: '$xs $sm',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
});

interface HomeListingsWidgetProps {
  onManage?: () => void;
}

export function HomeListingsWidget({ onManage }: HomeListingsWidgetProps) {
  const { offers: myOffers, loading } = useServiceOffers();

  if (loading && myOffers.length === 0) return null;
  if (myOffers.length === 0) return null;

  return (
    <Card css={{ padding: '$lg' }}>
      <Flex css={{ alignItems: 'center', gap: '$xs', marginBottom: '$md' }}>
        <SectionTitle>Services</SectionTitle>
        <CountBadge count={myOffers.length} />
        <Box css={{ flex: 1 }} />
        <Button variant="secondary" size="sm" onClick={onManage}>
          Manage
          <svg width="12" height="12" viewBox="0 0 16 16" fill="none">
            <path
              d="M3 8h10m-4-4 4 4-4 4"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </Button>
      </Flex>

      <Flex css={{ flexDirection: 'column', gap: '$xs' }}>
        {myOffers.map(offer => {
          const status = deriveStatus(offer);
          const label = deriveStatusLabel(offer);
          const isPaused = offer.paused || status === 'unsynced';
          const initial = offer.name[0]?.toUpperCase() ?? '#';

          return (
            <ListingRow
              key={`${offer.namespace}/${offer.name}`}
              css={{
                boxShadow: isPaused
                  ? 'inset 3px 0 0 0 $colors$obolGold'
                  : 'none',
              }}
            >
              {/* Name + endpoint */}
              <Flex css={{ alignItems: 'center', gap: '$xs', minWidth: 0 }}>
                <Avatar alt={offer.name} fallback={initial} />
                <Box css={{ minWidth: 0 }}>
                  <Flex css={{ alignItems: 'center', gap: '$xxs' }}>
                    <Text
                      css={{
                        color: '$light',
                        fontSize: '$3',
                        fontWeight: '$semibold',
                        fontFamily: 'monospace',
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                      }}
                    >
                      {offer.name}
                    </Text>
                    <Badge variant={offer.type === 'http' ? 'code' : 'purple'}>
                      {typeLabel(offer.type)}
                    </Badge>
                  </Flex>
                  <Text
                    css={{
                      fontSize: '$1',
                      color: '$muted',
                      fontFamily: 'monospace',
                      marginTop: 2,
                      whiteSpace: 'nowrap',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                    }}
                  >
                    {offer.endpoint || '\u2014'}
                  </Text>
                </Box>
              </Flex>

              {/* Status */}
              <StatusPill status={status} label={label} />

              {/* Traffic + earned */}
              <Box css={{ textAlign: 'right', lineHeight: 1.3 }}>
                {isPaused ? (
                  <Text
                    css={{
                      fontSize: '$1',
                      color: '$muted',
                      fontFamily: 'monospace',
                    }}
                  >
                    no traffic
                  </Text>
                ) : (
                  <>
                    {offer.usage?.requests24h != null && (
                      <Text
                        css={{
                          fontSize: '$1',
                          color: '$body',
                          fontFamily: 'monospace',
                        }}
                      >
                        {offer.usage.requests24h.toLocaleString()} req &middot;
                        24h
                      </Text>
                    )}
                    {offer.usage?.chargedRequests != null &&
                      offer.usage.chargedRequests > 0 && (
                        <Text
                          css={{
                            fontSize: '$1',
                            color: '$muted',
                            fontFamily: 'monospace',
                          }}
                        >
                          {offer.usage.chargedRequests.toLocaleString()}{' '}
                          {offer.usage.chargedRequests === 1 ? 'sale' : 'sales'}
                        </Text>
                      )}
                    {offer.usage?.lastSettlementUnix != null && (
                      <Text
                        css={{
                          fontSize: '$1',
                          color: '$muted',
                          fontFamily: 'monospace',
                        }}
                      >
                        last sale{' '}
                        {formatRelativeAgo(offer.usage.lastSettlementUnix)}
                      </Text>
                    )}
                  </>
                )}
              </Box>
            </ListingRow>
          );
        })}
      </Flex>
    </Card>
  );
}
