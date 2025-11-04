import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ChainChipProps {
  variant?: 'fullMode' | 'lightMode' | 'testnet' | 'mainnet';
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
      testnet: {
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
    case 'testnet':
      return 'Testnet';
    case 'mainnet':
      return 'Mainnet';
    default:
      return 'Mainnet';
  }
};

export function ChainChip({ variant = 'mainnet', className }: ChainChipProps) {
  return (
    <StyledChainChip variant={variant} className={className}>
      {getChipText(variant)}
    </StyledChainChip>
  );
}
