'use client';

import React, { useState, useEffect } from 'react';
import { Box, styled, keyframes } from '@obolnetwork/obol-ui';
import { useTiming } from '../../shared/TimingContext';

export interface AnimatedNumberProps {
  value: number;
  className?: string;
  animateLastDigit?: boolean;
  incrementInterval?: number; // in milliseconds
}

interface SpinningDigitProps {
  digit: number;
}

// Define the spin animation using Obol UI keyframes
const spinDigitAnimation = keyframes({
  '0%': {
    transform: 'rotateX(0deg)',
    opacity: 1,
  },
  '25%': {
    transform: 'rotateX(90deg)',
    opacity: 0.3,
  },
  '50%': {
    transform: 'rotateX(180deg)',
    opacity: 0.1,
  },
  '75%': {
    transform: 'rotateX(270deg)',
    opacity: 0.3,
  },
  '100%': {
    transform: 'rotateX(360deg)',
    opacity: 1,
  },
});

const DigitContainer = styled(Box, {
  display: 'inline-block',
  verticalAlign: 'baseline',
  minWidth: '1ch',
  textAlign: 'center',
  transformStyle: 'preserve-3d',
  backfaceVisibility: 'hidden',
  willChange: 'transform, opacity',

  variants: {
    isAnimating: {
      true: {
        animation: `${spinDigitAnimation} 600ms ease-in-out`,
      },
    },
  },
});

const NumberContainer = styled(Box, {
  display: 'inline-flex',
  alignItems: 'baseline',
});

const StaticPart = styled(Box, {
  display: 'inline-block',
});

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
    <DigitContainer isAnimating={isAnimating}>{displayDigit}</DigitContainer>
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
    return (
      <Box as="span" className={className}>
        {formattedNumber}
      </Box>
    );
  }

  // Split the number into all digits except the last one, and the last digit
  const digits = formattedNumber.replace(/,/g, '');
  const lastDigitIndex = digits.length - 1;
  const lastDigit = parseInt(digits[lastDigitIndex]) || 0;
  const beforeLastDigit = formattedNumber.slice(0, -1);

  return (
    <NumberContainer className={className} as="span">
      <StaticPart>{beforeLastDigit}</StaticPart>
      <SpinningDigit digit={lastDigit} />
    </NumberContainer>
  );
};
