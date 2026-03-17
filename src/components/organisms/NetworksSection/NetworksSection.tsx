'use client';

import { Box, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { SectionTitle } from '../../atoms/SectionTitle';
import { LocalNodeStatus } from '../LocalNodeStatus';
import { ErpcStatus } from '../ErpcStatus';
import {
  LocalNodeData,
  ErpcStatusData,
  ErpcNetwork,
  ErpcUpstream,
} from '../../../types/dashboard';
import {
  networkToChainName,
  groupUpstreamsByNetwork,
  sortNetworks,
} from '../../../lib/utils/network-rpc';

export interface NetworksSectionProps {
  localNodes: LocalNodeData[];
  erpcData: ErpcStatusData | null;
}

// ── Styled components ─────────────────────────────────────────────────────────

const SectionsList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$lg',
});

const ItemsList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  paddingTop: '$lg',
});

const LocalNodesList = styled(Box, {
  display: 'grid',
  gap: '$xs',
  gridTemplateColumns: '1fr',
  '@media (min-width: 640px)': {
    gridTemplateColumns: 'repeat(2, 1fr)',
  },
  paddingTop: '$lg',
});

// ── NetworksSection ───────────────────────────────────────────────────────────

export function NetworksSection({
  localNodes,
  erpcData,
}: NetworksSectionProps) {
  const erpcNetworks = sortNetworks(erpcData?.networks ?? []);
  const erpcUpstreamsByNetwork = erpcData
    ? groupUpstreamsByNetwork(erpcData.upstreams ?? [])
    : new Map();

  // Chains covered by eRPC
  const erpcChainNames = new Set(
    erpcNetworks.map(n => networkToChainName(n.alias)).filter(Boolean)
  );

  // eRPC entries with their embedded local nodes
  const erpcItems: {
    network: ErpcNetwork;
    upstreams?: ErpcUpstream[];
    localNodes: LocalNodeData[];
  }[] = erpcNetworks.map(n => ({
    network: n,
    upstreams: erpcUpstreamsByNetwork.get(n.id),
    localNodes: localNodes.filter(
      a => a.chainName === networkToChainName(n.alias)
    ),
  }));

  // Local nodes for chains not covered by eRPC, grouped by chain
  const localOnlyNodes = localNodes.filter(
    a => !erpcChainNames.has(a.chainName)
  );
  const localOnlyChains = [...new Set(localOnlyNodes.map(a => a.chainName))];

  const hasNetworks = erpcItems.length > 0;
  const hasLocalOnly = localOnlyChains.length > 0;

  return (
    <SectionsList>
      {hasNetworks && (
        <Card css={{ padding: '$lg' }}>
          <SectionTitle>Networks</SectionTitle>
          <ItemsList>
            {erpcItems.map(({ network, upstreams, localNodes: nodes }) => (
              <ErpcStatus
                key={network.id}
                network={network}
                upstreams={upstreams}
                localNodes={nodes}
              />
            ))}
          </ItemsList>
        </Card>
      )}

      {hasLocalOnly && (
        <Card css={{ padding: '$lg' }}>
          <SectionTitle>Applications</SectionTitle>
          <ItemsList>
            {localOnlyChains.map(chainName => (
              <LocalNodesList key={chainName}>
                {localOnlyNodes
                  .filter(a => a.chainName === chainName)
                  .map(app => (
                    <LocalNodeStatus key={app.appId} appData={app} />
                  ))}
              </LocalNodesList>
            ))}
          </ItemsList>
        </Card>
      )}
    </SectionsList>
  );
}
