'use client';

import { Box, styled } from '@obolnetwork/obol-ui';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { NetworkStatus } from '../NetworkStatus';
import { ErpcStatus } from '../ErpcStatus';
import {
  LocalNodeData,
  ErpcStatusData,
  ChainName,
  ErpcNetwork,
  ErpcUpstream,
} from '../../../types/dashboard';
import {
  sortNetworks,
  networkToChainName,
  groupUpstreamsByNetwork,
  NETWORK_ORDER,
} from '../../../lib/utils/network-rpc';

export interface NetworksSectionProps {
  localNodes: LocalNodeData[];
  remoteNode: ErpcStatusData | null;
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function collectChains(
  localNodes: LocalNodeData[],
  remoteNode: ErpcStatusData | null
): ChainName[] {
  const chains = new Set<ChainName>();

  localNodes.forEach(a => chains.add(a.chainName));

  remoteNode?.networks.forEach(n => {
    const cn = networkToChainName(n.alias);
    if (cn) chains.add(cn);
  });

  return NETWORK_ORDER.filter(c => chains.has(c as ChainName)) as ChainName[];
}

// ── Styled components ─────────────────────────────────────────────────────────

const ChainsList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const AppsList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  marginTop: '$md',
});

// ── NetworksSection ───────────────────────────────────────────────────────────

export function NetworksSection({
  localNodes,
  remoteNode,
}: NetworksSectionProps) {
  const chains = collectChains(localNodes, remoteNode);

  const erpcNetworks = remoteNode ? sortNetworks(remoteNode.networks) : [];
  const erpcUpstreamsByNetwork = remoteNode
    ? groupUpstreamsByNetwork(remoteNode.upstreams ?? [])
    : new Map();

  return (
    <Card css={{ padding: '$lg' }}>
      <SectionTitle>Networks</SectionTitle>
      <ChainsList css={{ paddingTop: '$lg' }}>
        {chains.map(chainName => {
          const erpcEntries: {
            network: ErpcNetwork;
            upstreams?: ErpcUpstream[];
          }[] = erpcNetworks
            .filter(n => networkToChainName(n.alias) === chainName)
            .map(n => ({
              network: n,
              upstreams: erpcUpstreamsByNetwork.get(n.id),
            }));

          const nodeApps = localNodes.filter(a => a.chainName === chainName);

          return (
            <Card key={chainName} css={{ backgroundColor: '$bg03' }}>
              <Accordion
                header={
                  <SectionTitle>
                    {chainName.charAt(0).toUpperCase() + chainName.slice(1)}
                  </SectionTitle>
                }
                defaultExpanded={true}
              >
                {erpcEntries.length > 0 && (
                  <AppsList>
                    {erpcEntries.map(({ network, upstreams }) => (
                      <ErpcStatus
                        key={network.id}
                        network={network}
                        upstreams={upstreams}
                      />
                    ))}
                  </AppsList>
                )}

                {nodeApps.length > 0 && (
                  <AppsList>
                    {nodeApps.map(app => (
                      <NetworkStatus key={app.appId} appData={app} />
                    ))}
                  </AppsList>
                )}
              </Accordion>
            </Card>
          );
        })}
      </ChainsList>
    </Card>
  );
}
