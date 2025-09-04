import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface BadgeProps {
  children: React.ReactNode;
  variant?: 'primary' | 'neutral' | 'success' | 'warning' | 'error';
  size?: 'sm' | 'md';
  className?: string;
}

const StyledBadge = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxs',
  borderRadius: '$pill',
  border: '1px solid',
  fontWeight: '$medium',
  transition: 'colors 300ms',

  variants: {
    variant: {
      neutral: {
        backgroundColor: '$bg02',
        color: '$light',
        borderColor: '$bg03',
      },
      success: {
        backgroundColor: 'rgba(47, 228, 171, 0.2)',
        color: '$obolGreen',
        borderColor: '$obolGreen',
      },
    },
    size: {
      sm: {
        padding: '$2 $xxs',
        fontSize: '$2',
      },
      md: {
        padding: '$xxxs $xs',
        fontSize: '$3',
      },
    },
  },

  defaultVariants: {
    variant: 'neutral',
    size: 'md',
  },
});

const StatusIndicator = styled(Box, {
  width: '$xxs',
  height: '$xxs',
  borderRadius: '$pill',
  backgroundColor: '$obolGreen',
});

export function Badge({
  children,
  variant = 'neutral',
  size = 'md',
  className,
}: BadgeProps) {
  return (
    <StyledBadge variant={variant} size={size} className={className}>
      {variant === 'success' && <StatusIndicator />}
      {children}
    </StyledBadge>
  );
}
