import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface PercentageChipProps {
  percentage: number;
  className?: string;
}

const getPercentageStyles = (percentage: number) => {
  if (percentage >= 0 && percentage <= 20) {
    return {
      background: 'rgba(47, 228, 171, 0.20)',
      color: '$obolGreen',
    };
  } else if (percentage >= 21 && percentage <= 40) {
    return {
      background: 'rgba(182, 234, 92, 0.20)',
      color: '$obolGreenLight',
    };
  } else if (percentage >= 41 && percentage <= 60) {
    return {
      background: 'rgba(232, 158, 48, 0.20)',
      color: '$obolGold',
    };
  } else if (percentage >= 61 && percentage <= 80) {
    return {
      background: 'rgba(221, 96, 60, 0.20)',
      color: '$test',
    };
  } else {
    // 81% till 100%
    return {
      background: 'rgba(204, 51, 51, 0.20)',
      color: '$red',
    };
  }
};

const StyledPercentageChip = styled(Box, {
  display: 'flex',
  width: '$2xl',
  height: '$lg',
  padding: '$1 $xs',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '$xxs',
  flexShrink: 0,
  borderRadius: '$3',
  fontFamily: '$fonts$body', // Uses design token instead of hardcoded value
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
  lineHeight: '$normal',
});

export function PercentageChip({ percentage, className }: PercentageChipProps) {
  const styles = getPercentageStyles(percentage);

  return (
    <StyledPercentageChip css={styles} className={className}>
      {Math.round(percentage)}%
    </StyledPercentageChip>
  );
}
