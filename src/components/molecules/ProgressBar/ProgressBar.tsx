import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ProgressBarProps {
  value: number; // 0-100
  variant?: 'critical' | 'warning' | 'caution' | 'good' | 'excellent';
}

const ProgressFill = styled(Box, {
  height: '2px',
  borderRadius: '80px',
  transition: 'width 500ms ease',

  variants: {
    variant: {
      critical: {
        backgroundColor: '#CC3333', // 0%-8%
      },
      warning: {
        backgroundColor: '#DD603C', // 9%-17%
      },
      caution: {
        backgroundColor: '#E89E30', // 18%-21%
      },
      good: {
        backgroundColor: '#B6EA5C', // 22%-31%
      },
      excellent: {
        backgroundColor: '#2FE4AB', // 32%-100% - Obol Green
      },
    },
  },

  defaultVariants: {
    variant: 'excellent',
  },
});

export const ProgressBar: React.FC<ProgressBarProps> = ({ value, variant }) => {
  // Auto-determine variant based on value if not provided
  const getVariant = (
    val: number
  ): 'critical' | 'warning' | 'caution' | 'good' | 'excellent' => {
    if (variant) return variant;
    if (val >= 0 && val <= 8) return 'critical';
    if (val >= 9 && val <= 17) return 'warning';
    if (val >= 18 && val <= 21) return 'caution';
    if (val >= 22 && val <= 31) return 'good';
    return 'excellent'; // 32% till 100%
  };

  const currentVariant = getVariant(value);
  const clampedValue = Math.min(Math.max(value, 0), 100);

  return (
    <ProgressFill
      variant={currentVariant}
      css={{ width: `${clampedValue}%` }}
    />
  );
};
