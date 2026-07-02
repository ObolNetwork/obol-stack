import React from 'react';
import { styled } from '@obolnetwork/obol-ui';
import { Button } from '../Button/Button';
import { tones } from '../../../lib/ui/tones';

export interface FilterChipProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  active?: boolean;
  children: React.ReactNode;
}

const StyledFilterChip = styled(Button, {
  variants: {
    active: {
      true: {
        background: tones.success.bg,
        borderColor: tones.success.border,
        color: tones.success.text,
        '&:hover:not(:disabled)': {
          color: tones.success.text,
          borderColor: tones.success.border,
        },
      },
    },
  },
});

export function FilterChip({ active, children, ...props }: FilterChipProps) {
  return (
    <StyledFilterChip
      type="button"
      variant="ghost"
      size="sm"
      pill
      active={active}
      {...props}
    >
      {children}
    </StyledFilterChip>
  );
}
