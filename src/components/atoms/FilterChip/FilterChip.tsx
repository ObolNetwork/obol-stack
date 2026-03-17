import React from 'react';
import { styled } from '@obolnetwork/obol-ui';

export interface FilterChipProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean;
  children: React.ReactNode;
}

const StyledFilterChip = styled('button', {
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$pill',
  color: '$body',
  fontSize: '$1',
  lineHeight: 1,
  fontWeight: '$medium',
  padding: '$xxxs $xs',
  cursor: 'pointer',
  transition: 'all 0.15s ease',
  '&:hover': { backgroundColor: '$obolDarkDark', color: '$obolGreenHover' },

  variants: {
    active: {
      true: {
        backgroundColor: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
      },
    },
  },
});

export function FilterChip({ active, children, ...props }: FilterChipProps) {
  return (
    <StyledFilterChip active={active} {...props}>
      {children}
    </StyledFilterChip>
  );
}
