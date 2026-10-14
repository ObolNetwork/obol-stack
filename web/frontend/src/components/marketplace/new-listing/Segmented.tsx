'use client';

import { styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';

const SegmentedRow = styled('div', {
  display: 'inline-flex',
  padding: 3,
  gap: 2,
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  width: '100%',
});

// Segment chip — Button atom (bare/sm) carrying only the active fill.
// Disabled options use the atom's built-in :disabled treatment.
const SegmentedButton = styled(Button, {
  flex: 1,

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        '&:hover:not(:disabled)': { color: '$obolGreen' },
      },
      false: { fontWeight: '$medium' },
    },
  },

  defaultVariants: {
    variant: 'bare',
    size: 'sm',
  },
});

export interface SegmentedOption<T extends string> {
  k: T;
  label: string;
  disabled?: boolean;
  title?: string;
}

export interface SegmentedProps<T extends string> {
  value: T;
  options: Array<SegmentedOption<T>>;
  onChange: (v: T) => void;
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
}: SegmentedProps<T>) {
  return (
    <SegmentedRow role="tablist">
      {options.map(opt => (
        <SegmentedButton
          key={opt.k}
          type="button"
          role="tab"
          aria-selected={opt.k === value}
          active={opt.k === value}
          disabled={opt.disabled}
          title={opt.title}
          onClick={() => {
            if (!opt.disabled) onChange(opt.k);
          }}
        >
          {opt.label}
        </SegmentedButton>
      ))}
    </SegmentedRow>
  );
}
