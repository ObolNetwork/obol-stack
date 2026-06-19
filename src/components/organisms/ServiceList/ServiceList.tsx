'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { SectionTitle } from '../../atoms/SectionTitle';
import { EmptyMessage } from '../../atoms/EmptyMessage';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { ServiceCard } from '../../molecules/ServiceCard';
import { useServiceOffers } from '../../../hooks/useServiceOffers';
import { useTunnelURL } from '../../../hooks/useAgents';
import { offerAnchorId } from '@/lib/services/agent-listing';
import type { ServiceOffer } from '@/types/sell';

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

const SubSectionTitle = styled(Text, {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',
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

export function ServiceList() {
  const { offers, pendingOffers, loading, error, refetch } = useServiceOffers();
  const { tunnelURL } = useTunnelURL();
  const { agentOffers, inferenceOffers, httpOffers } = partitionOffers(offers);
  const totalCount = offers.length + pendingOffers.length;

  const headerContent = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SectionTitle>Obol Services</SectionTitle>
      {!loading && totalCount > 0 && <CountBadge count={totalCount} />}
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
            <ErrorState message="Failed to load services" onRetry={refetch} />
          )}
          {!loading && !error && totalCount === 0 && (
            <EmptyMessage>
              No agent services for sale yet. Create a sub-agent, then hit{' '}
              <strong>Sell</strong> on its card — or run{' '}
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
                    <SubSectionTitle>Pending listings</SubSectionTitle>
                    <CountBadge count={pendingOffers.length} />
                    <SubSectionHint>agent</SubSectionHint>
                  </SubSectionHeader>
                  <SubSectionDesc>
                    ServiceOffer created but not live on the storefront yet.
                    Check conditions below for what is still reconciling.
                  </SubSectionDesc>
                  {pendingOffers.map(offer => (
                    <ServiceCard
                      key={`pending-${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={refetch}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))}
                </SubSection>
              ) : null}

              <SubSection>
                <SubSectionHeader>
                  <SubSectionTitle>Agent Services</SubSectionTitle>
                  {agentOffers.length > 0 && (
                    <CountBadge count={agentOffers.length} />
                  )}
                  <SubSectionHint>agent</SubSectionHint>
                </SubSectionHeader>
                <SubSectionDesc>
                  Live paid agent listings on your storefront (what buyers see).
                </SubSectionDesc>
                {agentOffers.length === 0 ? (
                  <EmptyMessage>No live agent services yet.</EmptyMessage>
                ) : (
                  agentOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={refetch}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))
                )}
              </SubSection>

              {inferenceOffers.length > 0 && (
                <SubSection>
                  <SubSectionHeader>
                    <SubSectionTitle>Inference Services</SubSectionTitle>
                    <CountBadge count={inferenceOffers.length} />
                    <SubSectionHint>inference</SubSectionHint>
                  </SubSectionHeader>
                  {inferenceOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={refetch}
                      anchorId={offerAnchorId(offer.namespace, offer.name)}
                    />
                  ))}
                </SubSection>
              )}

              {httpOffers.length > 0 && (
                <SubSection>
                  <SubSectionHeader>
                    <SubSectionTitle>API Services</SubSectionTitle>
                    <CountBadge count={httpOffers.length} />
                    <SubSectionHint>http</SubSectionHint>
                  </SubSectionHeader>
                  {httpOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                      onChanged={refetch}
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
