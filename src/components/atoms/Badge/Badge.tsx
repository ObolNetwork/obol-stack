import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';
import { CSS } from '@obolnetwork/obol-ui/dist/stitches.config';

export type BadgeVariant =
  | 'neutral'
  | 'green'
  | 'purple'
  | 'yellow'
  | 'red'
  | 'code';

export interface BadgeProps {
  children: React.ReactNode;
  variant?: BadgeVariant;
  className?: string;
  css?: CSS;
}

const StyledBadge = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  borderRadius: '$pill',
  padding: '$xxxs $xxs',
  fontSize: '$1',
  lineHeight: 1,
  fontWeight: '$medium',
  flexShrink: 0,

  variants: {
    variant: {
      neutral: {
        backgroundColor: '$bg03',
        color: '$body',
      },
      green: {
        backgroundColor: '#2FE4AB15',
        color: '$obolGreen',
      },
      purple: {
        backgroundColor: '#6366F115',
        color: '$coordinateHover',
      },
      yellow: {
        backgroundColor: '#F59E0B15',
        color: '$obolGold',
      },
      red: {
        backgroundColor: '#FF6B6B18',
        color: '$test',
      },
      code: {
        backgroundColor: '$bg01',
        color: '$body',
        border: '1px solid $stroke',
        fontFamily: 'monospace',
      },
    },
  },

  defaultVariants: {
    variant: 'neutral',
  },
});

export function Badge({ children, variant, className, css }: BadgeProps) {
  return (
    <StyledBadge variant={variant} className={className} css={css}>
      {children}
    </StyledBadge>
  );
}
