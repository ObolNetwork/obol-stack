'use client';

import { useEffect, useState } from 'react';
import { Flex, Text } from '@obolnetwork/obol-ui';

import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import { useWalletAssetBalances } from '@/hooks/useWalletAssetBalances';

import { Identicon } from '../Identicon';
import {
  ServicesPanel,
  FieldLabel,
  Mono,
  StripCell,
  StripGrid,
} from '../ServicesChrome';

function shortAddr(addr: string, prefix = 6, suffix = 4): string {
  if (addr.length <= prefix + suffix + 2) return addr;
  return `${addr.slice(0, prefix)}…${addr.slice(-suffix)}`;
}

// Maps our short chain key to (chainId, label, ChainIcon name).
const STRIP_CHAINS: ReadonlyArray<{
  key: 'mainnet' | 'base' | 'base-sepolia';
  chainId: number;
  label: string;
  icon: 'mainnet' | 'base' | 'base-sepolia';
}> = [
  { key: 'base', chainId: 8453, label: 'Base', icon: 'base' },
  {
    key: 'base-sepolia',
    chainId: 84532,
    label: 'Base Sepolia',
    icon: 'base-sepolia',
  },
  { key: 'mainnet', chainId: 1, label: 'Mainnet', icon: 'mainnet' },
];

function formatRelative(d: Date): string {
  const seconds = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  return `${h}h ago`;
}

export interface WalletStripProps {
  address: string | undefined;
  agentCount: number;
}

export function WalletStrip({ address, agentCount }: WalletStripProps) {
  // useWalletAssetBalances handles its own polling (60s) + per-chain
  // multicall3 batching via eRPC. When address is empty, the hook short-circuits
  // and returns an empty array — safe to call unconditionally per Rules of Hooks.
  const { balances } = useWalletAssetBalances(address ?? null);
  const [lastFetched, setLastFetched] = useState<Date | null>(null);
  useEffect(() => {
    if (balances.length > 0) setLastFetched(new Date());
  }, [balances]);

  const findBalance = (chainId: number, symbol: 'USDC' | 'OBOL'): string => {
    const hit = balances.find(
      b => b.chain.chainId === chainId && b.token.symbol === symbol
    );
    return hit?.formatted ?? '0';
  };

  return (
    <ServicesPanel css={{ padding: '$md' }}>
      <StripGrid>
        <StripCell first>
          <FieldLabel>
            Agent wallet{agentCount > 1 ? `s · ${agentCount}` : ''}
          </FieldLabel>
          {address ? (
            <Flex css={{ gap: 10, alignItems: 'center' }}>
              <Identicon seed={address} size={28} />
              <Mono
                css={{ fontSize: '$3', color: '$light', fontWeight: '$medium' }}
              >
                {shortAddr(address, 10, 8)}
              </Mono>
              <CopyButton text={address} />
            </Flex>
          ) : (
            <Text css={{ fontSize: '$2', color: '$muted' }}>
              {agentCount > 0
                ? 'Balances aggregate across agent wallets.'
                : 'No agent wallets discovered yet.'}
            </Text>
          )}
        </StripCell>

        {STRIP_CHAINS.map(c => {
          const usdc = findBalance(c.chainId, 'USDC');
          const obol = findBalance(c.chainId, 'OBOL');
          const empty = usdc === '0' && obol === '0';
          return (
            <StripCell key={c.key} css={empty ? { opacity: 0.55 } : {}}>
              <Flex css={{ gap: 6, alignItems: 'center' }}>
                <ChainIcon chain={c.icon} size={14} />
                <Text css={{ fontSize: '$1', color: '$body' }}>{c.label}</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{
                    fontSize: '$5',
                    color: empty ? '$muted' : '$light',
                    fontWeight: '$semibold',
                  }}
                >
                  {usdc}
                </Mono>
                <Text css={{ fontSize: '$1', color: '$body' }}>USDC</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{ fontSize: '$3', color: empty ? '$muted' : '$body' }}
                >
                  <ObolAmountSwitch amount={obol} symbol="OBOL" />
                </Mono>
              </Flex>
            </StripCell>
          );
        })}

        <StripCell
          last
          css={{
            borderLeft: '1px solid $stroke',
            paddingLeft: '$md',
            justifyContent: 'space-between',
          }}
        >
          <FieldLabel>Last fetched</FieldLabel>
          <Mono css={{ fontSize: '$2', color: '$light' }}>
            {lastFetched ? formatRelative(lastFetched) : '—'}
          </Mono>
        </StripCell>
      </StripGrid>
    </ServicesPanel>
  );
}
