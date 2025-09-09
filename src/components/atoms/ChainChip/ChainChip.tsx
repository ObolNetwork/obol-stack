import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ChainChipProps {
  variant?: 'fullMode' | 'lightMode' | 'testnet' | 'mainnet';
  className?: string;
}

const StyledChainChip = styled(Box, {
  display: 'inline-flex',
  height: '28px',
  padding: '4px 12px',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '8px',
  flexShrink: 0,
  borderRadius: '8px',
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '500',
  lineHeight: '18px',

  variants: {
    variant: {
      fullMode: {
        background: 'rgba(60, 210, 221, 0.20)',
        color: '#3CD2DD',
      },
      lightMode: {
        background: 'rgba(217, 238, 243, 0.20)',
        color: '#E1E9EB', // Text/Light
      },
      testnet: {
        border: '1px solid #2D4D53', // var(--Backgrounds-05, #2D4D53)
        background: '#111F22', // var(--Backgrounds-02, #111F22)
        color: '#E1E9EB', // Text/Light
      },
      mainnet: {
        background: '#E1E9EB', // var(--Text-Light, #E1E9EB)
        color: '#0A1518', // Backgrounds/01
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
