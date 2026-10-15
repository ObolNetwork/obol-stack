'use client';

import { Flex, Text, styled } from '@obolnetwork/obol-ui';

import { ChainIcon } from '@/components/atoms/ChainIcon';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';

import {
  ServicesPanel,
  FieldLabel,
  Mono,
  StripCell,
  StripGrid,
} from '../ServicesChrome';

const BigUSDC = styled('span', {
  fontFamily: 'monospace',
  fontSize: '$7',
  fontWeight: '$semibold',
  color: '$obolGreen',
  letterSpacing: '-0.02em',
});

export interface EarningsRollup {
  total7d: { USDC: string; OBOL: string };
  perChain: Record<
    'base' | 'sepolia' | 'mainnet',
    { USDC: string; OBOL: string }
  >;
  lastSettlement: string;
}

export function toRollupBucket(
  chain: string
): 'base' | 'sepolia' | 'mainnet' | null {
  const c = chain.toLowerCase();
  if (c === 'base' || c === 'eip155:8453') return 'base';
  if (c === 'base-sepolia' || c === 'sepolia' || c === 'eip155:84532')
    return 'sepolia';
  if (c === 'mainnet' || c === 'ethereum' || c === 'eip155:1') return 'mainnet';
  return null;
}

export function formatAgo(d: Date): string {
  const seconds = Math.max(0, Math.floor((Date.now() - d.getTime()) / 1000));
  if (seconds < 60) return `${seconds}s ago`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function EarningsStrip({ rollup }: { rollup: EarningsRollup }) {
  return (
    <ServicesPanel css={{ padding: '$md' }}>
      <StripGrid>
        <StripCell first>
          <FieldLabel>Earned · last 7d</FieldLabel>
          <Flex css={{ alignItems: 'baseline', gap: 6 }}>
            <BigUSDC>{rollup.total7d.USDC}</BigUSDC>
            <Text css={{ fontSize: '$2', color: '$body' }}>USDC</Text>
          </Flex>
          <Flex css={{ alignItems: 'baseline', gap: 6 }}>
            <Mono css={{ fontSize: '$3', color: '$body' }}>
              <ObolAmountSwitch
                amount={rollup.total7d.OBOL}
                symbol="OBOL"
                prefix="+"
              />
            </Mono>
          </Flex>
        </StripCell>

        {(['base', 'sepolia', 'mainnet'] as const).map(c => {
          const { USDC, OBOL } = rollup.perChain[c];
          const empty = parseFloat(USDC) === 0 && parseFloat(OBOL) === 0;
          const label = c === 'sepolia' ? 'base-sepolia' : c;
          return (
            <StripCell key={c} css={empty ? { opacity: 0.55 } : {}}>
              <Flex css={{ gap: 6, alignItems: 'center' }}>
                <ChainIcon chain={label} size={14} />
                <Text css={{ fontSize: '$1', color: '$body' }}>
                  {c === 'sepolia'
                    ? 'Base Sepolia'
                    : c === 'base'
                      ? 'Base'
                      : 'Mainnet'}
                </Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{
                    fontSize: '$5',
                    color: empty ? '$muted' : '$light',
                    fontWeight: '$semibold',
                  }}
                >
                  {USDC}
                </Mono>
                <Text css={{ fontSize: '$1', color: '$body' }}>USDC</Text>
              </Flex>
              <Flex css={{ alignItems: 'baseline', gap: 6 }}>
                <Mono
                  css={{ fontSize: '$3', color: empty ? '$muted' : '$body' }}
                >
                  <ObolAmountSwitch amount={OBOL} symbol="OBOL" />
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
          <FieldLabel>Last settlement</FieldLabel>
          <Mono css={{ fontSize: '$2', color: '$light' }}>
            {rollup.lastSettlement}
          </Mono>
        </StripCell>
      </StripGrid>
    </ServicesPanel>
  );
}
