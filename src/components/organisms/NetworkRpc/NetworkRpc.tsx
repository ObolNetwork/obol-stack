'use client';

import React, { useState, useRef, useEffect, MouseEvent } from 'react';
import { WalletClient } from 'viem';
import { Box, Flex, Text, styled, theme } from '@obolnetwork/obol-ui';
import { useWalletClient } from 'wagmi';
import { Up } from '@/icons';
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

// ── Header ────────────────────────────────────────────────────────────────────

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

// ── Network cards ─────────────────────────────────────────────────────────────

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
  cursor: 'pointer',
  userSelect: 'none',
  '&:hover [data-chevron]': { color: '$light' },
});

// Fixed-width chip wrapper so all rows align regardless of chain name length
const ChipSlot = styled(Box, {
  minWidth: 76,
  flexShrink: 0,
});

const TestnetBadge = styled(Text, {
  fontSize: '$1',
  fontWeight: '$medium',
  color: '$muted',
  textTransform: 'uppercase',
  letterSpacing: '0.06em',
  flexShrink: 0,
});

const HeaderSpacer = styled(Box, { flex: 1 });

const ChevronWrap = styled(Box, {
  width: 20,
  height: 20,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  transition: 'transform 0.25s ease',
  // Up icon points up; rotate 180° when collapsed so it points down
  transform: 'rotate(180deg)',
  variants: {
    expanded: {
      true: { transform: 'rotate(0deg)' },
    },
  },
});

// ── Expanded body ─────────────────────────────────────────────────────────────

const ExpandBody = styled(Box, {
  overflow: 'hidden',
  transition: 'opacity 0.25s ease',
  variants: {
    expanded: {
      true: { opacity: 1 },
      false: { opacity: 0 },
    },
  },
});

const BodyInner = styled(Box, {
  borderTop: '1px solid $stroke',
  padding: '$xs $sm $sm',
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxs',
});

const DetailRow = styled(Flex, {
  alignItems: 'baseline',
  gap: '$sm',
});

const DetailLabel = styled(Text, {
  fontSize: '$2',
  color: '$muted',
  flexShrink: 0,
  width: 64,
});

const DetailValue = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontFamily: 'monospace',
  wordBreak: 'break-all',
});

// ── Helpers ───────────────────────────────────────────────────────────────────

const TESTNETS = new Set(['hoodi', 'sepolia']);
const NETWORK_ORDER = ['mainnet', 'sepolia', 'hoodi'];

function sortNetworks(networks: ErpcNetwork[]): ErpcNetwork[] {
  return [...networks].sort((a, b) => {
    const ai = NETWORK_ORDER.indexOf(a.alias);
    const bi = NETWORK_ORDER.indexOf(b.alias);
    if (ai === -1 && bi === -1) return a.alias.localeCompare(b.alias);
    if (ai === -1) return 1;
    if (bi === -1) return -1;
    return ai - bi;
  });
}

function networkToChainName(alias: string): ChainName | null {
  if (alias === 'mainnet' || alias === 'sepolia' || alias === 'hoodi') {
    return alias as ChainName;
  }
  return null;
}

function networkToStatus(state: string): ClientStatusType {
  return state === 'OK' ? 'synced' : 'error';
}

function chainIdFromEvmId(id: string): number | null {
  const match = id.match(/^evm:(\d+)$/);
  return match ? parseInt(match[1], 10) : null;
}

function erpcUrl(): string {
  return process.env.NEXT_PUBLIC_ERPC_URL ?? 'https://obol.stack/rpc';
}

// ── NetworkRow with expand/collapse ──────────────────────────────────────────

function NetworkRow({
  network,
  walletClient,
}: {
  network: ErpcNetwork;
  walletClient: WalletClient | null | undefined;
}) {
  const [expanded, setExpanded] = useState(false);
  const bodyRef = useRef<HTMLDivElement>(null);

  const chainName = networkToChainName(network.alias);
  const status = networkToStatus(network.state);
  const isTestnet = TESTNETS.has(network.alias);
  const chainId = chainIdFromEvmId(network.id);
  const endpoint = `${erpcUrl()}/${network.alias}`;

  // Animate height on expand/collapse (same pattern as Accordion)
  useEffect(() => {
    const el = bodyRef.current;
    if (!el) return;
    if (expanded) {
      const h = el.scrollHeight;
      el.style.height = '0px';
      requestAnimationFrame(() => {
        el.style.height = `${h}px`;
      });
    } else {
      if (el.style.height === 'auto') {
        el.style.height = `${el.scrollHeight}px`;
        requestAnimationFrame(() => {
          el.style.height = '0px';
        });
      } else {
        el.style.height = '0px';
      }
    }
  }, [expanded]);

  const handleTransitionEnd = () => {
    if (expanded && bodyRef.current) {
      bodyRef.current.style.height = 'auto';
    }
  };

  return (
    <NetworkCardWrapper>
      <NetworkCardHeader onClick={() => setExpanded(e => !e)}>
        <ChipSlot>
          <ChainChip variant={chainName ?? 'mainnet'} />
        </ChipSlot>

        {isTestnet && <TestnetBadge>testnet</TestnetBadge>}

        <StatusPill status={status} />

        <HeaderSpacer />

        {/* Stop click propagation so wallet button doesn't toggle accordion */}
        <Box onClick={(e: MouseEvent) => e.stopPropagation()}>
          <AddNetworkButton
            chainName={chainName ?? 'mainnet'}
            walletClient={chainName ? walletClient : null}
          />
        </Box>

        <ChevronWrap expanded={expanded} data-chevron>
          <Up size="s" color={theme.colors.body.value} />
        </ChevronWrap>
      </NetworkCardHeader>

      <ExpandBody
        ref={bodyRef}
        expanded={expanded}
        onTransitionEnd={handleTransitionEnd}
        style={{ height: 0 }}
      >
        <BodyInner>
          <DetailRow>
            <DetailLabel>Endpoint</DetailLabel>
            <DetailValue>{endpoint}</DetailValue>
          </DetailRow>
          {chainId !== null && (
            <DetailRow>
              <DetailLabel>Chain ID</DetailLabel>
              <DetailValue>{chainId}</DetailValue>
            </DetailRow>
          )}
        </BodyInner>
      </ExpandBody>
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
