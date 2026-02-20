import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { CountBadge } from '../../atoms/CountBadge';
import { NetworkStatus } from '../NetworkStatus';
import { NetworkRpc } from '../NetworkRpc';
import { AppData } from '../../../types/dashboard';

export interface NetworksSectionProps {
  apps: AppData[];
}

const SectionTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

const AddNetworkButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  height: '$xl',
  padding: '$xxxs $xs',
  borderRadius: '$3',
  border: '1px dashed $stroke',
  background: 'transparent',
  color: '$body',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontWeight: '$medium',
  cursor: 'not-allowed',
  opacity: 0.5,
  flexShrink: 0,
});

const NetworksList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  paddingTop: '$lg',
});

export function NetworksSection({ apps }: NetworksSectionProps) {
  const headerContent = (
    <Flex
      css={{
        alignItems: 'center',
        gap: '$sm',
        paddingRight: '$xl',
      }}
    >
      <SectionTitle>Networks</SectionTitle>
      <CountBadge count={apps.length} />
      {/* <AddNetworkButton disabled title="Coming soon">
        + Add network
      </AddNetworkButton> */}
    </Flex>
  );

  return (
    <Card css={{ position: 'relative', overflow: 'hidden', padding: '0' }}>
      <Accordion header={headerContent} defaultExpanded={true}>
        <NetworksList>
          {apps.map(app =>
            app.app === 'erpc' ? (
              <NetworkRpc key={app.appId} appData={app} />
            ) : (
              <NetworkStatus key={app.appId} appData={app} />
            )
          )}
        </NetworksList>
      </Accordion>
    </Card>
  );
}
