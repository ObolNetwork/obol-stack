import React from 'react';
import { Box, styled, theme } from '@obolnetwork/obol-ui';
import { Tooltip } from '../Tooltip';

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
        width: '$sm',
        height: '$sm',
      },
      md: {
        width: '$md',
        height: '$md',
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
      return theme.colors.obolGreen.value;
    case 'unsynced':
      return theme.colors.body.value;
    default:
      return theme.colors.obolGreen.value;
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
  const tooltipText = status === 'synced' ? 'Synced' : 'Unsynced';

  return (
    <Tooltip content={tooltipText} side="bottom">
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
    </Tooltip>
  );
}
