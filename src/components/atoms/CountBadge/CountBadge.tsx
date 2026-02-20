import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface CountBadgeProps {
  count: number;
  className?: string;
}

const BadgeRoot = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  minWidth: '22px',
  height: '22px',
  padding: '0 7px',
  borderRadius: '6px',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  backgroundColor: 'rgba(47, 228, 171, 0.08)',
  color: '#2FE4AB',
  fontSize: '11px',
  fontWeight: '$semibold',
  fontVariantNumeric: 'tabular-nums',
  letterSpacing: '0.02em',
  lineHeight: 1,
  flexShrink: 0,
  transition: 'border-color 150ms ease, background-color 150ms ease',
});

export function CountBadge({ count, className }: CountBadgeProps) {
  return <BadgeRoot className={className}>{count}</BadgeRoot>;
}
