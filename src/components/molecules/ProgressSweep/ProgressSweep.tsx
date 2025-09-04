'use client';

import React from 'react';
import { Box, styled, keyframes } from '@obolnetwork/obol-ui';

export interface ProgressSweepProps {
  duration?: number; // Duration in milliseconds
  className?: string;
  color?: string;
}

// Define the keyframe animations using Obol UI
const progressFillAnimation = keyframes({
  '0%': {
    width: '0.1%',
    opacity: 0.8,
    transform: 'scaleX(0)',
  },
  '1%': {
    width: '0.1%',
    opacity: 1,
    transform: 'scaleX(0.001)',
  },
  '99%': {
    width: '99.9%',
    opacity: 1,
    transform: 'scaleX(1)',
  },
  '100%': {
    width: '100%',
    opacity: 0.8,
    transform: 'scaleX(1)',
  },
});

const progressGlowAnimation = keyframes({
  '0%': {
    width: '0.1%',
    opacity: 0.6,
    transform: 'scaleX(0)',
  },
  '2%': {
    width: '0.2%',
    opacity: 0.8,
    transform: 'scaleX(0.002)',
  },
  '98%': {
    width: '99.8%',
    opacity: 0.8,
    transform: 'scaleX(1)',
  },
  '100%': {
    width: '100%',
    opacity: 0.6,
    transform: 'scaleX(1)',
  },
});

const ProgressContainer = styled(Box, {
  position: 'relative',
  width: '100%',
  height: '4px',
  minHeight: '4px',
  borderRadius: '$pill',
  overflow: 'hidden',

  variants: {
    rounded: {
      false: {
        borderRadius: 0,
      },
    },
  },
});

const ProgressBackground = styled(Box, {
  position: 'absolute',
  inset: 0,
  borderRadius: '$pill',
  background:
    'linear-gradient(90deg, rgba(113, 113, 122, 0.2) 0%, rgba(82, 82, 91, 0.3) 100%)',
});

const ProgressFill = styled(Box, {
  position: 'absolute',
  left: 0,
  top: 0,
  bottom: 0,
  borderRadius: '$pill',
  width: '0%',
  transformOrigin: 'left center',
  backfaceVisibility: 'hidden',
  willChange: 'width, opacity, transform',
});

const ProgressGlow = styled(Box, {
  position: 'absolute',
  left: 0,
  top: 0,
  bottom: 0,
  borderRadius: '$pill',
  width: '0%',
  filter: 'blur(1px)',
  transformOrigin: 'left center',
  backfaceVisibility: 'hidden',
  willChange: 'width, opacity, transform',
});

export function ProgressSweep({
  duration = 12000,
  className,
  color = '$obolGreen',
}: ProgressSweepProps) {
  // Handle legacy color values
  const resolvedColor = color === 'var(--obol-green)' ? '$obolGreen' : color;
  const isRounded = !className?.includes('rounded-none');

  return (
    <ProgressContainer className={className} rounded={isRounded}>
      <ProgressBackground />

      <ProgressFill
        css={{
          background: `linear-gradient(90deg, ${resolvedColor} 0%, ${resolvedColor} 50%, ${resolvedColor} 100%)`,
          boxShadow: `0 0 8px ${resolvedColor}, inset 0 1px 0 ${resolvedColor}`,
          animation: `${progressFillAnimation} ${duration}ms linear infinite`,
        }}
      />

      <ProgressGlow
        css={{
          background: `linear-gradient(90deg, ${resolvedColor} 0%, ${resolvedColor} 50%, ${resolvedColor} 100%)`,
          opacity: 0.6,
          animation: `${progressGlowAnimation} ${duration}ms linear infinite`,
        }}
      />
    </ProgressContainer>
  );
}
