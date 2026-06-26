'use client';

import { useState } from 'react';
import { Flex, Text, styled, OpenInNew } from '@obolnetwork/obol-ui';
import { CopyButton } from '../../atoms/CopyButton';
import type { AssetBalance } from '../../../hooks/useWalletAssetBalances';
import { explorerUrlFor } from '@/lib/config/agent-balance-tokens';
import { groupBalancesByChain } from '@/lib/wallet/sort-balances';

const BalancesList = styled(Flex, {
  flexDirection: 'column',
  width: '100%',
  variants: {
    compact: {
      true: {
        gap: '4px',
        marginTop: 0,
      },
      false: {
        gap: '$xxxs',
        marginTop: '$xs',
      },
    },
  },
  defaultVariants: {
    compact: false,
  },
});

const BalanceRow = styled(Flex, {
  alignItems: 'center',
  width: '100%',
  variants: {
    compact: {
      true: {
        gap: '$xxs',
        fontSize: '$1',
      },
      false: {
        gap: '$xs',
        fontSize: '$1',
      },
    },
  },
  defaultVariants: {
    compact: false,
  },
});

const NetworkRow = styled(Flex, {
  alignItems: 'center',
  gap: '$xxs',
  flexWrap: 'wrap',
  width: '100%',
});

const TokenPair = styled(Text, {
  fontSize: '$1',
  color: '$light',
  fontFamily: 'monospace',
  whiteSpace: 'nowrap',
});

const TokenSymbol = styled('span', {
  fontWeight: '$semibold',
  marginRight: '3px',
});

const ChainHint = styled(Text, {
  fontSize: '$1',
  color: '$body',
  textTransform: 'lowercase',
  whiteSpace: 'nowrap',
  flexShrink: 0,
  marginLeft: 'auto',
});

const ExpandChip = styled('button', {
  fontSize: '$1',
  color: '$obolGreen',
  background: 'transparent',
  border: 'none',
  padding: 0,
  cursor: 'pointer',
  fontFamily: 'inherit',
  whiteSpace: 'nowrap',
  '&:hover': { textDecoration: 'underline' },
});

const BalanceSymbol = styled(Text, {
  fontWeight: '$semibold',
  color: '$light',
  fontFamily: 'monospace',
  flexShrink: 0,
  fontSize: '11px',
  minWidth: 38,
});

const BalanceAmount = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
  flex: 1,
  minWidth: 0,
  textAlign: 'left',
  fontSize: '$1',
});

const ExplorerLink = styled('a', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: 22,
  height: 22,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: 'transparent',
  color: '$body',
  cursor: 'pointer',
  transition:
    'color 150ms ease, border-color 150ms ease, background 150ms ease',
  '&:hover': {
    color: '$light',
    borderColor: '$body',
    background: '$bg05',
  },
});

const TOKEN_INLINE_LIMIT = 3;

function CompactNetworkBalances({
  walletAddress,
  balances,
}: {
  walletAddress: string;
  balances: AssetBalance[];
}) {
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? balances : balances.slice(0, TOKEN_INLINE_LIMIT);
  const hidden = expanded
    ? 0
    : Math.max(0, balances.length - TOKEN_INLINE_LIMIT);
  const chainLabel = balances[0]?.chain.shortName ?? '';

  return (
    <NetworkRow>
      {visible.map(b => (
        <TokenPair key={`${b.chain.chainId}-${b.token.symbol}`}>
          <TokenSymbol>{b.token.symbol}</TokenSymbol>
          {b.formatted}
        </TokenPair>
      ))}
      {hidden > 0 && (
        <ExpandChip
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded(true)}
        >
          +{hidden}
        </ExpandChip>
      )}
      {expanded && balances.length > TOKEN_INLINE_LIMIT && (
        <ExpandChip
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded(false)}
        >
          less
        </ExpandChip>
      )}
      <ChainHint>{chainLabel}</ChainHint>
      {!expanded &&
        balances
          .slice(0, 1)
          .filter(b => b.token.address)
          .map(b => (
            <ExplorerLink
              key={`${b.chain.chainId}-explorer`}
              href={explorerUrlFor(b.chain, b.token, walletAddress)}
              target="_blank"
              rel="noopener noreferrer"
              title={`View on ${b.chain.chainName} explorer`}
              aria-label={`View wallet on ${b.chain.chainName} explorer`}
            >
              <OpenInNew size="sm" />
            </ExplorerLink>
          ))}
    </NetworkRow>
  );
}

interface WalletBalanceListProps {
  walletAddress: string;
  balances: AssetBalance[];
  compact?: boolean;
}

export function WalletBalanceList({
  walletAddress,
  balances,
  compact = false,
}: WalletBalanceListProps) {
  if (balances.length === 0) return null;

  if (compact) {
    const groups = groupBalancesByChain(balances);
    return (
      <BalancesList compact>
        {groups.map(group => (
          <CompactNetworkBalances
            key={group[0].chain.chainId}
            walletAddress={walletAddress}
            balances={group}
          />
        ))}
      </BalancesList>
    );
  }

  return (
    <BalancesList compact={false}>
      {balances.map(b => {
        const key = `${b.chain.chainId}-${b.token.symbol}`;
        const url = explorerUrlFor(b.chain, b.token, walletAddress);
        return (
          <BalanceRow key={key} compact={false}>
            <BalanceSymbol>{b.token.symbol}</BalanceSymbol>
            <BalanceAmount>{b.formatted}</BalanceAmount>
            <ChainHint>{b.chain.shortName}</ChainHint>
            {b.token.address && (
              <CopyButton
                text={b.token.address}
                title={`Copy ${b.token.symbol} contract`}
              />
            )}
            <ExplorerLink
              href={url}
              target="_blank"
              rel="noopener noreferrer"
              title={`View on ${b.chain.chainName} explorer`}
              aria-label={`View ${b.token.symbol} on ${b.chain.chainName} explorer`}
            >
              <OpenInNew size="sm" />
            </ExplorerLink>
          </BalanceRow>
        );
      })}
    </BalancesList>
  );
}
