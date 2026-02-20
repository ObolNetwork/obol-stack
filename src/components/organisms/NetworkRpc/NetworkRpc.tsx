'use client';

import React, { MouseEvent } from 'react';
import { WalletClient } from 'viem';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useWalletClient } from 'wagmi';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { AppIcon } from '../../atoms/AppIcon';
import { CopyButton } from '../../atoms/CopyButton';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import {
  AppData,
  ErpcNetwork,
  ChainName,
  ClientStatusType,
} from '../../../types/dashboard';
import {
  TESTNETS,
  sortNetworks,
  networkToChainName,
  networkToStatus,
  chainIdFromEvmId,
  erpcUrl,
} from '../../../lib/utils/network-rpc';

export interface NetworkRpcProps {
  appData: AppData;
}

// ── Header ────────────────────────────────────────────────────────────────────

const RpcHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  marginBottom: '$md',
});

const CardTitle = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  lineHeight: '$base',
});

// ── Network list ──────────────────────────────────────────────────────────────

const NetworkList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const NetworkCardWrapper = styled(Box, {
  borderRadius: '$3',
  background: '$bg04',
  border: '1px solid $stroke',
  overflow: 'hidden',
});

const NetworkCardHeader = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
  padding: '$xs $sm',
});

const ChipSlot = styled(Box, {
  minWidth: 76,
  flexShrink: 0,
});

const TestnetBadge = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  flexShrink: 0,
});

const HeaderSpacer = styled(Box, { flex: 1 });

// ── Details section ───────────────────────────────────────────────────────────

const DetailsSection = styled(Box, {
  borderTop: '1px solid $stroke',
  padding: '$xs $sm $sm',
  display: 'grid',
  gridTemplateColumns: '72px 1fr',
  columnGap: '$sm',
  rowGap: '$xxs',
  alignItems: 'baseline',
});

// $body = #9CC2C9 — clearly readable on dark bg, properly secondary
const DetailLabel = styled(Text, {
  fontSize: '$1',
  fontWeight: '$semibold',
  color: '$body',
  textTransform: 'uppercase',
  letterSpacing: '$wider',
  whiteSpace: 'nowrap',
});

// $light = #D9EEF3 — bright and clearly legible
const DetailValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
  lineHeight: '$base',
});

const EndpointRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xs',
  minWidth: 0,
});

// ── NetworkRow ────────────────────────────────────────────────────────────────

function NetworkRow({
  network,
  walletClient,
}: {
  network: ErpcNetwork;
  walletClient: WalletClient | null | undefined;
}) {
  const chainName = networkToChainName(network.alias);
  const status = networkToStatus(network.state);
  const isTestnet = TESTNETS.has(network.alias);
  const chainId = chainIdFromEvmId(network.id);
  const endpoint = `${erpcUrl()}/${network.alias}`;

  return (
    <NetworkCardWrapper>
      <NetworkCardHeader>
        <ChipSlot>
          <ChainChip variant={chainName ?? 'mainnet'} />
        </ChipSlot>

        {isTestnet && <TestnetBadge>testnet</TestnetBadge>}

        <HeaderSpacer />

        <StatusPill status={status} />

        <Box onClick={(e: MouseEvent) => e.stopPropagation()}>
          <AddNetworkButton
            chainName={chainName ?? 'mainnet'}
            walletClient={chainName ? walletClient : null}
          />
        </Box>
      </NetworkCardHeader>

      <DetailsSection>
        <DetailLabel>Endpoint</DetailLabel>
        <EndpointRow>
          <DetailValue>{endpoint}</DetailValue>
          <CopyButton text={endpoint} title="Copy endpoint" />
        </EndpointRow>

        {chainId !== null && (
          <>
            <DetailLabel>Chain ID</DetailLabel>
            <DetailValue>{chainId}</DetailValue>
          </>
        )}
      </DetailsSection>
    </NetworkCardWrapper>
  );
}

// ── Main component ────────────────────────────────────────────────────────────

export function NetworkRpc({ appData }: NetworkRpcProps) {
  const { data: walletClient } = useWalletClient();
  const networks = sortNetworks(appData.erpcStatus?.networks ?? []);

  return (
    <Card
      css={{
        position: 'relative',
        overflow: 'hidden',
        backgroundColor: '$bg03',
        borderRadius: '$2',
        padding: '$sm $md',
      }}
    >
      <RpcHeader>
        <Flex css={{ alignItems: 'center', gap: '$xs' }}>
          <AppIcon app="ethereum" size={20} />
          <CardTitle>Network RPC</CardTitle>
        </Flex>
        <StatusPill status={appData.status} />
      </RpcHeader>

      <NetworkList>
        {networks.map(network => (
          <NetworkRow
            key={network.id}
            network={network}
            walletClient={walletClient}
          />
        ))}
      </NetworkList>
    </Card>
  );
}
