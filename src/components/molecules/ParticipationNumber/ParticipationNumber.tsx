'use client';

import React, { useState, useEffect } from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';
import { AnimatedNumber } from '../AnimatedNumber';
import { useTiming } from '../../shared/TimingContext';
import { randomWalkStep, RandomWalkConfig } from '../../../lib/randomWalkUtils';

export interface ParticipationNumberProps {
  initialValue: number;
  target?: number; // Target mean value (default: 97.5)
  standardDeviation?: number; // Standard deviation (default: 1)
  updateInterval?: number; // Update interval in milliseconds (default: 4000)
  className?: string;
  animateLastDigit?: boolean;
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
        color: '#F59E0B', // Yellow warning color
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
  initialValue,
  target = 97.5,
  standardDeviation = 1,
  updateInterval = 4000,
  className = '',
}) => {
  const [currentValue, setCurrentValue] = useState(initialValue);
  const { peerTick, isActive } = useTiming();

  useEffect(() => {
    if (updateInterval === 4000 && isActive) {
      // Use centralized peer timing for 4-second intervals
      setCurrentValue(prev => {
        const config: RandomWalkConfig = {
          target,
          standardDeviation,
          minValue: Math.max(94.5, target - 3 * standardDeviation),
          maxValue: 100.0, // Hard cap at 100%
          precision: 1, // 1 decimal place for percentages
          meanReversionStrength: 0.3,
          varianceScale: 0.15,
        };

        const newValue = randomWalkStep(prev, config);

        // Additional safety check to prevent any possibility of exceeding 100%
        return Math.min(100.0, Math.max(94.5, newValue));
      });
    } else {
      // Fallback to individual intervals for custom timing
      const interval = setInterval(() => {
        setCurrentValue(prev => {
          const config: RandomWalkConfig = {
            target,
            standardDeviation,
            minValue: Math.max(94.5, target - 3 * standardDeviation),
            maxValue: 100.0, // Hard cap at 100%
            precision: 1, // 1 decimal place for percentages
            meanReversionStrength: 0.3,
            varianceScale: 0.15,
          };

          const newValue = randomWalkStep(prev, config);

          // Additional safety check to prevent any possibility of exceeding 100%
          return Math.min(100.0, Math.max(94.5, newValue));
        });
      }, updateInterval);

      return () => clearInterval(interval);
    }
  }, [target, standardDeviation, updateInterval, peerTick, isActive]);

  // Determine status based on value
  const getStatus = (value: number): 'healthy' | 'warning' | 'normal' => {
    if (value < 96.5) {
      return 'warning'; // Yellow warning for low participation
    }
    return 'healthy'; // Green for healthy participation
  };

  // Force 100% cap on display value to prevent any display issues
  const displayValue = Math.min(100.0, currentValue);
  const status = getStatus(displayValue);

  return (
    <ParticipationContainer className={className} status={status} as="span">
      <AnimatedNumber
        value={displayValue}
        animateLastDigit={false} // DISABLE to prevent AnimatedNumber from adding blockTick
      />
      <PercentageSymbol as="span">%</PercentageSymbol>
    </ParticipationContainer>
  );
};
