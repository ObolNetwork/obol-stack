'use client';

import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface ParticipationNumberProps {
  value: number;
  className?: string;
}

const ParticipationContainer = styled(Box, {
  display: 'inline-flex',
  alignItems: 'baseline',
  gap: '$1',

  variants: {
    status: {
      healthy: {
        color: '$obolGreen',
      },
      warning: {
        color: '$obolGold',
      },
      normal: {
        color: 'inherit',
      },
    },
  },

  defaultVariants: {
    status: 'normal',
  },
});

const PercentageSymbol = styled(Box, {
  fontSize: 'inherit',
  fontWeight: 'inherit',
});

export const ParticipationNumber: React.FC<ParticipationNumberProps> = ({
  value,
  className = '',
}) => {
  // Use the real API value directly
  const displayValue = Math.min(100.0, value);

  // Determine status based on value
  const getStatus = (value: number): 'healthy' | 'warning' | 'normal' => {
    if (value < 96.5) {
      return 'warning'; // Yellow warning for low participation
    }
    return 'healthy'; // Green for healthy participation
  };

  const status = getStatus(displayValue);

  return (
    <ParticipationContainer className={className} status={status} as="span">
      {displayValue.toFixed(1)}
      <PercentageSymbol as="span">%</PercentageSymbol>
    </ParticipationContainer>
  );
};
