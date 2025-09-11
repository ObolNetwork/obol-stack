import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ProgressBarProps {
  value: number; // 0-100
  variant?: 'critical' | 'warning' | 'caution' | 'good' | 'excellent';
}

const ProgressFill = styled(Box, {
  height: '2px',
  borderRadius: '$pill',
  transition: 'width 500ms ease',

  variants: {
    variant: {
      critical: {
        backgroundColor: '$test', // 0%-8% - Red for critical
      },
      warning: {
        backgroundColor: '$testHover', // 9%-17% - Orange for warning
      },
      caution: {
        backgroundColor: '$obolGold', // 18%-21% - Gold for caution
      },
      good: {
        backgroundColor: '$obolGreenLight', // 22%-31% - Light green for good
      },
      excellent: {
        backgroundColor: '$obolGreen', // 32%-100% - Obol Green for excellent
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
