import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface StatusIndicatorProps {
  status?: 'synced' | 'unsynced';
  size?: 'sm' | 'md';
  className?: string;
}

const StatusContainer = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',

  variants: {
    size: {
      sm: {
        width: '16px',
        height: '16px',
      },
      md: {
        width: '20px',
        height: '20px',
      },
    },
  },

  defaultVariants: {
    size: 'md',
  },
});

const getStatusColors = (status: StatusIndicatorProps['status']) => {
  switch (status) {
    case 'synced':
      return '#2FE4AB'; // Primary - Obol Green/Mid
    case 'unsynced':
      return '#97B2B8'; // Text/Body
    default:
      return '#2FE4AB'; // Primary - Obol Green/Mid
  }
};

const getSizeValues = (size: StatusIndicatorProps['size']) => {
  switch (size) {
    case 'sm':
      return { width: 16, height: 16, outerRadius: 8, innerRadius: 4 };
    case 'md':
      return { width: 20, height: 20, outerRadius: 10, innerRadius: 5 };
    default:
      return { width: 20, height: 20, outerRadius: 10, innerRadius: 5 };
  }
};

export function StatusIndicator({
  status = 'synced',
  size = 'md',
  className,
}: StatusIndicatorProps) {
  const color = getStatusColors(status);
  const sizeValues = getSizeValues(size);

  return (
    <StatusContainer size={size} className={className}>
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width={sizeValues.width}
        height={sizeValues.height}
        viewBox={`0 0 ${sizeValues.width} ${sizeValues.height}`}
        fill="none"
      >
        <circle
          opacity="0.3"
          cx={sizeValues.outerRadius}
          cy={sizeValues.outerRadius}
          r={sizeValues.outerRadius}
          fill={color}
        />
        <circle
          cx={sizeValues.outerRadius}
          cy={sizeValues.outerRadius}
          r={sizeValues.innerRadius}
          fill={color}
        />
      </svg>
    </StatusContainer>
  );
}
