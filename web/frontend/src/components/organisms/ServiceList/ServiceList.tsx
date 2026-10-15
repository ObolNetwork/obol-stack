'use client';

import { useRouter } from 'next/navigation';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { ServiceCard } from '../../molecules/ServiceCard';
import { useTunnelURL } from '../../../hooks/useAgents';
import { offerAnchorId } from '@/lib/services/agent-listing';
import type { ServiceOffer } from '@/types/sell';

export interface ServiceListProps {
  offers: ServiceOffer[];
  pendingOffers: ServiceOffer[];
  loading: boolean;
  error: string | null;
  onRefresh: () => void;
}

const SubSection = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',

  '& + &': {
    marginTop: '$lg',
  },
});

const SubSectionHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  paddingBottom: '$xs',
  borderBottom: '1px dashed $stroke',
});

const SubSectionHint = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontFamily: 'monospace',
});

const SubSectionDesc = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  marginBottom: '$xs',
});

const ListBody = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

function partitionOffers(offers: ServiceOffer[]) {
  const agentOffers = offers.filter(o => o.type === 'agent');
  const inferenceOffers = offers.filter(
    o => o.type === 'inference' || o.type === 'fine-tuning'
  );
  const httpOffers = offers.filter(o => o.type === 'http');
  return { agentOffers, inferenceOffers, httpOffers };
}

export function ServiceList({
  offers,
  pendingOffers,
  loading,
  error,
  onRefresh,
}: ServiceListProps) {
  const router = useRouter();
  const { tunnelURL } = useTunnelURL();
  const { agentOffers, inferenceOffers, httpOffers } = partitionOffers(offers);
  const totalCount = offers.length + pendingOffers.length;

  const headerContent = (
    <Flex
      css={{
        alignItems: 'center',
        gap: '$sm',
        paddingRight: '$xl',
        width: '100%',
      }}
    >
      <SectionTitle>Services on Sale</SectionTitle>
      {!loading && totalCount > 0 && <CountBadge count={totalCount} />}
      <Box css={{ marginLeft: 'auto' }}>
        <Button
          variant="secondary"
          size="sm"
          onClick={() => router.push('/marketplace/listings')}
        >
          Manage
        </Button>
      </Box>
    </Flex>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <ListBody>
          {loading && totalCount === 0 && (
            <LoadingState size="small" message="Loading services..." />
          )}
          {error && (
            <ErrorState message="Failed to load services" onRetry={onRefresh} />
          )}
          {!loading && !error && totalCount === 0 && (
            <EmptyMessage>
              Nothing on sale yet. Create an agent, then run{' '}
              <code>obol sell agent &lt;name&gt;</code> from Terminal.
            </EmptyMessage>
          )}

          {(agentOffers.length > 0 ||
            inferenceOffers.length > 0 ||
            pendingOffers.length > 0 ||
            httpOffers.length > 0) && (
            <>
              {pendingOffers.length > 0 ? (
                <SubSection>
                  <SubSectionHeader>
                    <SectionTitle level={2}>Pending</SectionTitle>
                    <CountBadge count={pendingOffers.length} />
                    <SubSectionHint>agent</SubSectionHint>
                  </SubSectionHeader>
                  <SubSectionDesc>
                    Created but not live on your storefront yet.
                  </SubSectionDesc>
                  {pendingOffers.map(offer => (
                    <ServiceCard
                      key={`pending-${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={onRefresh}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))}
                </SubSection>
              ) : null}

              <SubSection>
                <SubSectionHeader>
                  <SectionTitle level={2}>Agents for sale</SectionTitle>
                  {agentOffers.length > 0 && (
                    <CountBadge count={agentOffers.length} />
                  )}
                  <SubSectionHint>agent</SubSectionHint>
                </SubSectionHeader>
                <SubSectionDesc>
                  Live on your storefront — what buyers see.
                </SubSectionDesc>
                {agentOffers.length === 0 ? (
                  <EmptyMessage>No agents on sale yet.</EmptyMessage>
                ) : (
                  agentOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={onRefresh}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))
                )}
              </SubSection>

              {inferenceOffers.length > 0 && (
                <SubSection>
                  <SubSectionHeader>
                    <SectionTitle level={2}>Inference for sale</SectionTitle>
                    <CountBadge count={inferenceOffers.length} />
                    <SubSectionHint>inference</SubSectionHint>
                  </SubSectionHeader>
                  {inferenceOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={onRefresh}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))}
                </SubSection>
              )}

              {httpOffers.length > 0 && (
                <SubSection>
                  <SubSectionHeader>
                    <SectionTitle level={2}>
                      HTTP services for sale
                    </SectionTitle>
                    <CountBadge count={httpOffers.length} />
                    <SubSectionHint>http</SubSectionHint>
                  </SubSectionHeader>
                  {httpOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={onRefresh}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))}
                </SubSection>
              )}
            </>
          )}
        </ListBody>
      </Accordion>
    </Card>
  );
}
