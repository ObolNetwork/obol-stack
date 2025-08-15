'use client';

import React, { useState, useEffect } from 'react';
import { AnimatedNumber } from './AnimatedNumber';
import { useTiming } from './TimingContext';
import { randomWalkStep, RandomWalkConfig } from './randomWalkUtils';

interface RandomWalkNumberProps {
  initialValue: number;
  target: number; // Target mean value (e.g., 72)
  standardDeviation: number; // Standard deviation (e.g., 10)
  updateInterval: number; // Update interval in milliseconds (e.g., 4000)
  className?: string;
  animateLastDigit?: boolean;
  minValue?: number;
  maxValue?: number;
}

export const RandomWalkNumber: React.FC<RandomWalkNumberProps> = ({
  initialValue,
  target,
  standardDeviation,
  updateInterval,
  className = '',
  animateLastDigit = false,
  minValue,
  maxValue,
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
          minValue: minValue ?? Math.max(0, target - 3 * standardDeviation),
          maxValue: maxValue ?? target + 3 * standardDeviation,
          precision: 0, // Integer values for peer counts
          meanReversionStrength: 0.25,
          varianceScale: 0.15
        };
        
        return randomWalkStep(prev, config);
      });
    } else {
      // Fallback to individual intervals for custom timing
      const interval = setInterval(() => {
        setCurrentValue(prev => {
          const config: RandomWalkConfig = {
            target,
            standardDeviation,
            minValue: minValue ?? Math.max(0, target - 3 * standardDeviation),
            maxValue: maxValue ?? target + 3 * standardDeviation,
            precision: 0, // Integer values for peer counts
            meanReversionStrength: 0.25,
            varianceScale: 0.15
          };
          
          return randomWalkStep(prev, config);
        });
      }, updateInterval);

      return () => clearInterval(interval);
    }
  }, [target, standardDeviation, updateInterval, minValue, maxValue, peerTick, isActive]);

  return (
    <AnimatedNumber 
      value={currentValue}
      className={className}
      animateLastDigit={animateLastDigit}
    />
  );
};