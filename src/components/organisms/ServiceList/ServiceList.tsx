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

const ListBody = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
  paddingTop: '$lg',
});

function partitionOffers(offers: ServiceOffer[]) {
  const agentOffers = offers.filter(
    o => o.type === 'inference' || o.type === 'fine-tuning'
  );
  const httpOffers = offers.filter(o => o.type === 'http');
  return { agentOffers, httpOffers };
}

export function ServiceList() {
  const { offers, loading, error } = useServiceOffers();
  const { tunnelURL } = useTunnelURL();
  const { agentOffers, httpOffers } = partitionOffers(offers);

  const headerContent = (
    <Flex css={{ alignItems: 'center', gap: '$sm', paddingRight: '$xl' }}>
      <SectionTitle>Obol Services</SectionTitle>
      {!loading && offers.length > 0 && <CountBadge count={offers.length} />}
    </Flex>
  );

  return (
    <Card section>
      <Accordion header={headerContent} defaultExpanded={true}>
        <ListBody>
          {loading && offers.length === 0 && (
            <LoadingState size="small" message="Loading services..." />
          )}
          {error && <ErrorState message="Failed to load services" />}
          {!loading && !error && offers.length === 0 && (
            <EmptyMessage>
              No services for sale yet. Run <code>obol sell http …</code> or{' '}
              <code>obol sell inference …</code> to publish one.
            </EmptyMessage>
          )}

          {(agentOffers.length > 0 || httpOffers.length > 0) && (
            <>
              <SubSection>
                <SubSectionHeader>
                  <SubSectionTitle>Agent Services</SubSectionTitle>
                  {agentOffers.length > 0 && (
                    <CountBadge count={agentOffers.length} />
                  )}
                  <SubSectionHint>inference · fine-tuning</SubSectionHint>
                </SubSectionHeader>
                {agentOffers.length === 0 ? (
                  <EmptyMessage>No agent services for sale.</EmptyMessage>
                ) : (
                  agentOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                    />
                  ))
                )}
              </SubSection>

              <SubSection>
                <SubSectionHeader>
                  <SubSectionTitle>API Services</SubSectionTitle>
                  {httpOffers.length > 0 && (
                    <CountBadge count={httpOffers.length} />
                  )}
                  <SubSectionHint>http</SubSectionHint>
                </SubSectionHeader>
                {httpOffers.length === 0 ? (
                  <EmptyMessage>No API services for sale.</EmptyMessage>
                ) : (
                  httpOffers.map(offer => (
                    <ServiceCard
                      key={`${offer.namespace}/${offer.name}`}
                      offer={offer}
                      tunnelURL={tunnelURL}
                    />
                  ))
                )}
              </SubSection>
            </>
          )}
        </ListBody>
      </Accordion>
    </Card>
  );
}
