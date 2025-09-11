import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface PercentageChipProps {
  percentage: number;
  className?: string;
}

const getPercentageStyles = (percentage: number) => {
  if (percentage >= 0 && percentage <= 8) {
    return {
      background: 'rgba(221, 96, 60, 0.20)', // $test with 20% opacity
      color: '$test',
    };
  } else if (percentage >= 9 && percentage <= 17) {
    return {
      background: 'rgba(232, 156, 135, 0.20)', // $testHover with 20% opacity
      color: '$testHover',
    };
  } else if (percentage >= 18 && percentage <= 21) {
    return {
      background: 'rgba(232, 158, 48, 0.20)', // $obolGold with 20% opacity
      color: '$obolGold',
    };
  } else if (percentage >= 22 && percentage <= 31) {
    return {
      background: 'rgba(182, 234, 92, 0.20)', // $obolGreenLight with 20% opacity
      color: '$obolGreenLight',
    };
  } else {
    // 32% till 100%
    return {
      background: 'rgba(47, 228, 171, 0.20)', // $obolGreen with 20% opacity
      color: '$obolGreen',
    };
  }
};

const StyledPercentageChip = styled(Box, {
  display: 'flex',
  width: '40px',
  height: '$lg',
  padding: '$1 $xs',
  justifyContent: 'center',
  alignItems: 'center',
  gap: '$xxs',
  flexShrink: 0,
  borderRadius: '$3',
  fontFamily: '"DM Sans"',
  fontSize: '$2',
  fontStyle: 'normal',
  fontWeight: '$semibold',
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
