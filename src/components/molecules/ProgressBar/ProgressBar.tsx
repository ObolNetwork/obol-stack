import React from 'react';
import { Box } from '@obolnetwork/obol-ui';
import { styled } from '@/lib/stitches.config';

export interface ProgressBarProps {
  value: number; // 0-100
  variant?: 'excellent' | 'good' | 'warning' | 'critical' | 'severe';
}

const ProgressFill = styled(Box, {
  height: '2px',
  borderRadius: '$pill',
  transition: 'width 500ms ease',

  variants: {
    variant: {
      excellent: {
        backgroundColor: '$obolGreen', // 0%-20% - Green for excellent (low usage)
      },
      good: {
        backgroundColor: '$obolGreenLight', // 21%-40% - Light green for good
      },
      warning: {
        backgroundColor: '$orange', // 41%-60% - Orange for warning
      },
      critical: {
        backgroundColor: '$test', // 61%-80% - Test color for critical
      },
      severe: {
        backgroundColor: '$red', // 81%-100% - Red for severe
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
  ): 'excellent' | 'good' | 'warning' | 'critical' | 'severe' => {
    if (variant) return variant;
    if (val >= 0 && val <= 20) return 'excellent';
    if (val >= 21 && val <= 40) return 'good';
    if (val >= 41 && val <= 60) return 'warning';
    if (val >= 61 && val <= 80) return 'critical';
    return 'severe'; // 81% till 100%
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
