import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';

export interface CountBadgeProps {
  count?: number;
  children?: React.ReactNode;
  className?: string;
}

const BadgeRoot = styled(Box, {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  minWidth: '22px',
  height: '22px',
  padding: '0 7px',
  borderRadius: '$2',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  backgroundColor: 'rgba(47, 228, 171, 0.08)',
  color: '$obolGreen',
  fontSize: '$1',
  fontWeight: '$semibold',
  fontVariantNumeric: 'tabular-nums',
  letterSpacing: '0.02em',
  lineHeight: 1,
  flexShrink: 0,
  transition: 'border-color 150ms ease, background-color 150ms ease',
});

export function CountBadge({ count, children, className }: CountBadgeProps) {
  return <BadgeRoot className={className}>{children ?? count}</BadgeRoot>;
}
