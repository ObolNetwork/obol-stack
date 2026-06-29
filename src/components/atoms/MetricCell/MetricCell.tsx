import type { ReactNode } from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';

const Cell = styled(Box, {
  minWidth: '88px',
});

const Label = styled(Text, {
  fontSize: '$1',
  color: '$body',
  lineHeight: 1.3,
});

const Value = styled(Text, {
  fontSize: '$2',
  color: '$light',
  fontWeight: '$semibold',
  fontVariantNumeric: 'tabular-nums',
  lineHeight: 1.3,
  marginTop: '2px',
});

export interface MetricCellProps {
  label: string;
  value: ReactNode;
}

/** Compact label + value pair for card stat rows. */
export function MetricCell({ label, value }: MetricCellProps) {
  return (
    <Cell>
      <Label>{label}</Label>
      <Value>{value}</Value>
    </Cell>
  );
}
