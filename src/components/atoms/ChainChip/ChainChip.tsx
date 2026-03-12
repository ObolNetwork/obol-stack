import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ChainChipProps {
  variant?:
    | 'fullMode'
    | 'lightMode'
    | 'aztecSequencer'
    | 'erpcProxy'
    | 'hoodi'
    | 'sepolia'
    | 'mainnet';
  label?: string;
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
      return 'Full Mode';
    case 'lightMode':
      return 'Light Mode';
    case 'hoodi':
      return 'Hoodi';
    case 'sepolia':
      return 'Sepolia';
    case 'mainnet':
      return 'Mainnet';
    case 'aztecSequencer':
      return 'Sequencer';
    case 'erpcProxy':
      return 'RPC Proxy';
    default:
      return 'Mainnet';
  }
};

export function ChainChip({
  variant = 'mainnet',
  label,
  className,
}: ChainChipProps) {
  return (
    <StyledChainChip variant={variant} className={className}>
      {label ?? getChipText(variant)}
    </StyledChainChip>
  );
}
