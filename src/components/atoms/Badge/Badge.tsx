import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';
import { CSS } from '@obolnetwork/obol-ui/dist/stitches.config';
import { tones } from '../../../lib/ui/tones';

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
        backgroundColor: tones.success.bg,
        color: '$obolGreen',
      },
      // No semantic tone for purple — protocol/type chips keep their own hue.
      purple: {
        backgroundColor: '#6366F115',
        color: '$coordinateHover',
      },
      yellow: {
        backgroundColor: tones.warning.bg,
        color: '$obolGold',
      },
      red: {
        backgroundColor: tones.danger.bg,
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
