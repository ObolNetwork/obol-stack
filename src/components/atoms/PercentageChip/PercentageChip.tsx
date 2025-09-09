import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface PercentageChipProps {
  percentage: number;
  className?: string;
}

const getPercentageStyles = (percentage: number) => {
  if (percentage >= 0 && percentage <= 8) {
    return {
      background: 'rgba(204, 51, 51, 0.20)',
      color: '#CC3333',
    };
  } else if (percentage >= 9 && percentage <= 17) {
    return {
      background: 'rgba(221, 96, 60, 0.20)',
      color: '#DD603C',
    };
  } else if (percentage >= 18 && percentage <= 21) {
    return {
      background: 'rgba(232, 158, 48, 0.20)',
      color: '#E89E30',
    };
  } else if (percentage >= 22 && percentage <= 31) {
    return {
      background: 'rgba(182, 234, 92, 0.20)',
      color: '#B6EA5C',
    };
  } else {
    // 32% till 100%
    return {
      background: 'rgba(47, 228, 171, 0.20)',
      color: '#2FE4AB', // Primary - Obol Green/Mid
    };
  }
};

const StyledPercentageChip = styled(Box, {
  display: 'flex',
  width: '40px',
  height: '24px',
  padding: '4px 12px',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '8px',
  flexShrink: 0,
  borderRadius: '8px',
  fontFamily: '"DM Sans"',
  fontSize: '12px',
  fontStyle: 'normal',
  fontWeight: '600',
  lineHeight: 'normal',
});

export function PercentageChip({ percentage, className }: PercentageChipProps) {
  const styles = getPercentageStyles(percentage);

  return (
    <StyledPercentageChip css={styles} className={className}>
      {Math.round(percentage)}%
    </StyledPercentageChip>
  );
}
