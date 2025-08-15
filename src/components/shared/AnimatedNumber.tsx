'use client';

import React, { useState, useEffect } from 'react';
import { useTiming } from './TimingContext';

interface AnimatedNumberProps {
  value: number;
  className?: string;
  animateLastDigit?: boolean;
  incrementInterval?: number; // in milliseconds
}

interface SpinningDigitProps {
  digit: number;
}

const SpinningDigit: React.FC<SpinningDigitProps> = ({ digit }) => {
  const [isAnimating, setIsAnimating] = useState(false);
  const [displayDigit, setDisplayDigit] = useState(digit);

  useEffect(() => {
    if (displayDigit !== digit) {
      setIsAnimating(true);
      // Delay the digit change to sync with animation
      const timeout = setTimeout(() => {
        setDisplayDigit(digit);
        setIsAnimating(false);
      }, 300); // Half of the animation duration

      return () => clearTimeout(timeout);
    }
  }, [digit, displayDigit]);

  return (
    <span 
      className={`
        inline-block align-baseline
        ${isAnimating ? 'animate-spin-digit' : ''}
      `}
      style={{ 
        minWidth: '1ch',
        textAlign: 'center'
      }}
    >
      {displayDigit}
    </span>
  );
};

export const AnimatedNumber: React.FC<AnimatedNumberProps> = ({
  value,
  className = '',
  animateLastDigit = false,
  incrementInterval = 12000,
}) => {
  const [currentValue, setCurrentValue] = useState(value);
  const { blockTick, isActive } = useTiming();

  useEffect(() => {
    if (animateLastDigit && incrementInterval === 12000 && isActive) {
      // Use centralized block timing for 12-second intervals
      setCurrentValue(value + blockTick);
    } else {
      // For all other cases, just use the value as-is
      // The animateLastDigit only affects the visual digit animation, not the actual value
      setCurrentValue(value);
    }
  }, [value, animateLastDigit, incrementInterval, blockTick, isActive]);

  const formattedNumber = currentValue.toLocaleString();
  
  if (!animateLastDigit) {
    return <span className={className}>{formattedNumber}</span>;
  }

  // Split the number into all digits except the last one, and the last digit
  const digits = formattedNumber.replace(/,/g, '');
  const lastDigitIndex = digits.length - 1;
  const lastDigit = parseInt(digits[lastDigitIndex]) || 0;
  const beforeLastDigit = formattedNumber.slice(0, -1);

  return (
    <span className={`${className} inline-flex items-baseline`}>
      <span className="inline-block">{beforeLastDigit}</span>
      <SpinningDigit digit={lastDigit} />
    </span>
  );
};