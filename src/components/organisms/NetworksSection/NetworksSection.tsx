'use client';

import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { WalletClient } from 'viem';
import { useWalletClient } from 'wagmi';
import { Card } from '../../atoms/Card';
import { Accordion } from '../../molecules/Accordion';
import { SectionTitle } from '../../atoms/SectionTitle';
import { NetworkStatus } from '../NetworkStatus';
import { ErpcStatus } from '../ErpcStatus';
import {
  AppData,
  ChainName,
  ErpcNetwork,
  ErpcUpstream,
  NODE_MODES,
} from '../../../types/dashboard';
import {
  sortNetworks,
  networkToChainName,
  groupUpstreamsByNetwork,
  NETWORK_ORDER,
} from '../../../lib/utils/network-rpc';

export interface NetworksSectionProps {
  apps: AppData[];
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function collectChains(apps: AppData[]): ChainName[] {
  const chains = new Set<ChainName>();

  apps
    .filter(a => a.nodeMode !== NODE_MODES.ERPC_PROXY)
    .forEach(a => chains.add(a.chainName));

  apps
    .filter(a => a.nodeMode === NODE_MODES.ERPC_PROXY)
    .forEach(erpcApp =>
      erpcApp.erpcStatus?.networks.forEach(n => {
        const cn = networkToChainName(n.alias);
        if (cn) chains.add(cn);
      })
    );

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
  gap: '$xs',
  marginTop: '$xs',
});

// ── NetworksSection ───────────────────────────────────────────────────────────

export function NetworksSection({ apps }: NetworksSectionProps) {
  const { data: walletClient } = useWalletClient();

  const chains = collectChains(apps);

  const erpcAppData = apps
    .filter(a => a.nodeMode === NODE_MODES.ERPC_PROXY)
    .map(app => ({
      networks: sortNetworks(app.erpcStatus?.networks ?? []),
      upstreamsByNetwork: groupUpstreamsByNetwork(
        app.erpcStatus?.upstreams ?? []
      ),
    }));

  return (
    <Card css={{ padding: '$lg' }}>
      <SectionTitle>Networks</SectionTitle>
      <ChainsList css={{ paddingTop: '$lg' }}>
        {chains.map(chainName => {
          const erpcEntries: {
            network: ErpcNetwork;
            upstreams?: ErpcUpstream[];
          }[] = erpcAppData.flatMap(({ networks, upstreamsByNetwork }) =>
            networks
              .filter(n => networkToChainName(n.alias) === chainName)
              .map(n => ({
                network: n,
                upstreams: upstreamsByNetwork.get(n.id),
              }))
          );

          const nodeApps = apps.filter(
            a =>
              a.chainName === chainName && a.nodeMode !== NODE_MODES.ERPC_PROXY
          );

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
                        walletClient={walletClient}
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
