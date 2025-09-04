'use client';

import React, { useState, useEffect } from 'react';
import { AnimatedNumber } from './AnimatedNumber';
import { useTiming } from './TimingContext';
import { randomWalkStep, RandomWalkConfig } from './randomWalkUtils';

interface ParticipationNumberProps {
  initialValue: number;
  target?: number; // Target mean value (default: 97.5)
  standardDeviation?: number; // Standard deviation (default: 1)
  updateInterval?: number; // Update interval in milliseconds (default: 4000)
  className?: string;
  animateLastDigit?: boolean;
}

export const ParticipationNumber: React.FC<ParticipationNumberProps> = ({
  initialValue,
  target = 97.5,
  standardDeviation = 1,
  updateInterval = 4000,
  className = '',
  animateLastDigit = false,
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

  // Determine styling based on value
  const getValueStyle = (value: number) => {
    if (value < 96.5) {
      return 'text-yellow-400'; // Yellow warning for low participation
    }
    return 'text-[var(--obol-green)]'; // Green for healthy participation
  };

  // Force 100% cap on display value to prevent any display issues
  const displayValue = Math.min(100.0, currentValue);

  return (
    <div className={`${getValueStyle(displayValue)} ${className}`}>
      <AnimatedNumber
        value={displayValue}
        animateLastDigit={false} // DISABLE to prevent AnimatedNumber from adding blockTick
      />
      <span>%</span>
    </div>
  );
};
