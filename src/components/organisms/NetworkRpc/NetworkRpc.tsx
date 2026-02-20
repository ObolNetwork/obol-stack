'use client';

import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { useWalletClient } from 'wagmi';
import { Card } from '../../atoms/Card';
import { ChainChip } from '../../atoms/ChainChip';
import { StatusPill } from '../../atoms/StatusIndicator';
import { AppIcon } from '../../atoms/AppIcon';
import { AddNetworkButton } from '../../molecules/AddNetworkButton';
import {
  AppData,
  ErpcNetwork,
  ChainName,
  ClientStatusType,
} from '../../../types/dashboard';

export interface NetworkRpcProps {
  appData: AppData;
}

// ── Layout ───────────────────────────────────────────────────────────────────

const RpcHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: '$sm',
  marginBottom: '$lg',
});

const CardTitle = styled(Text, {
  fontSize: '$4',
  fontWeight: '$semibold',
  color: '$light',
  lineHeight: '$base',
});

const NetworkList = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xs',
});

const NetworkRow = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
});

const RowSpacer = styled(Box, { flex: 1 });

// ── Helpers ──────────────────────────────────────────────────────────────────

function networkToChainName(alias: string): ChainName | null {
  if (alias === 'mainnet' || alias === 'sepolia' || alias === 'hoodi') {
    return alias as ChainName;
  }
  return null;
}

function networkToStatus(state: string): ClientStatusType {
  return state === 'OK' ? 'synced' : 'error';
}

// ── Main component ────────────────────────────────────────────────────────────

export function NetworkRpc({ appData }: NetworkRpcProps) {
  const { data: walletClient } = useWalletClient();
  const networks: ErpcNetwork[] = appData.erpcStatus?.networks ?? [];

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
        {networks.map((network: ErpcNetwork) => {
          const chainName = networkToChainName(network.alias);
          const status = networkToStatus(network.state);

          return (
            <NetworkRow key={network.id}>
              {chainName ? (
                <ChainChip variant={chainName} />
              ) : (
                <ChainChip variant="mainnet" />
              )}
              <StatusPill status={status} />
              <RowSpacer />
              <AddNetworkButton
                chainName={chainName ?? 'mainnet'}
                walletClient={chainName ? walletClient : null}
              />
            </NetworkRow>
          );
        })}
      </NetworkList>
    </Card>
  );
}
