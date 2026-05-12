import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ChainChipProps {
  variant?:
    | 'fullMode'
    | 'lightMode'
    | 'aztecSequencer'
    | 'erpcProxy'
    | 'remoteNode'
    | 'hoodi'
    | 'sepolia'
    | 'mainnet';
  label?: string;
  small?: boolean;
  className?: string;
}

const StyledChainChip = styled(Box, {
  display: 'inline-flex',
  height: '$xl', // 28px approximated to 32px
  padding: '$xxxs $xs',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '$xxs',
  flexShrink: 0,
  borderRadius: '$3',
  fontFamily: '$fonts$body',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$medium',
  lineHeight: '$base',

  variants: {
    variant: {
      fullMode: {
        background: 'rgba(60, 210, 221, 0.20)',
        color: '#3CD2DD',
      },
      lightMode: {
        background: 'rgba(217, 238, 243, 0.20)',
        color: '$light',
      },
      aztecSequencer: {
        background: 'rgba(212, 255, 40, 0.20)',
        color: '#D4FF28',
      },
      erpcProxy: {
        background: 'rgba(145, 103, 228, 0.30)',
        color: '#C4A8F5',
      },
      remoteNode: {
        background: 'rgba(91, 158, 255, 0.15)',
        color: '#5B9EFF',
      },
      hoodi: {
        border: '1px solid $bg05',
        background: '$bg02',
        color: '$light',
      },
      sepolia: {
        border: '1px solid $bg05',
        background: '$bg02',
        color: '$light',
      },
      mainnet: {
        background: '$light',
        color: '$bg01',
      },
    },
  },

  defaultVariants: {
    variant: 'mainnet',
  },
});

const getChipText = (variant: ChainChipProps['variant']) => {
  switch (variant) {
    case 'fullMode':
      return 'Local Full Mode';
    case 'lightMode':
      return 'Local Light Mode';
    case 'hoodi':
      return 'Hoodi';
    case 'sepolia':
      return 'Sepolia';
    case 'mainnet':
      return 'Ethereum';
    case 'aztecSequencer':
      return 'Sequencer';
    case 'remoteNode':
      return 'Remote Node';
    case 'erpcProxy':
      return 'RPC';
    default:
      return 'Ethereum';
  }
};

export function ChainChip({
  variant = 'mainnet',
  label,
  small,
  className,
}: ChainChipProps) {
  return (
    <StyledChainChip
      variant={variant}
      className={className}
      css={
        small
          ? { height: 'auto', padding: '$xxxs $xxs', fontSize: '$1' }
          : undefined
      }
    >
      {label ?? getChipText(variant)}
    </StyledChainChip>
  );
}
