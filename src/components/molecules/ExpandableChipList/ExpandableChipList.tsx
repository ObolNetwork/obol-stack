'use client';

import { useState } from 'react';
import { Flex, Text, styled } from '@obolnetwork/obol-ui';

const ChipRow = styled(Flex, {
  flexWrap: 'wrap',
  gap: '$xxs',
});

const Chip = styled(Text, {
  fontSize: '$1',
  color: '$body',
  backgroundColor: 'rgba(255, 255, 255, 0.04)',
  border: '1px solid rgba(255, 255, 255, 0.08)',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

const ToggleChip = styled('button', {
  fontSize: '$1',
  color: '$obolGreen',
  backgroundColor: 'rgba(47, 228, 171, 0.06)',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
  cursor: 'pointer',
  fontFamily: 'inherit',

  '&:hover': {
    backgroundColor: 'rgba(47, 228, 171, 0.12)',
    borderColor: 'rgba(47, 228, 171, 0.45)',
  },
});

export interface ExpandableChipListProps {
  items: string[];
  /** Prefix each chip label (e.g. "Domain: "). */
  labelPrefix?: string;
  /** Max chips before "+N more". Default 3. */
  limit?: number;
}

export function ExpandableChipList({
  items,
  labelPrefix = '',
  limit = 3,
}: ExpandableChipListProps) {
  const [expanded, setExpanded] = useState(false);
  if (items.length === 0) return null;

  const visible = expanded ? items : items.slice(0, limit);
  const hidden = expanded ? 0 : Math.max(0, items.length - limit);

  return (
    <ChipRow>
      {visible.map(item => (
        <Chip key={item}>
          {labelPrefix}
          {item}
        </Chip>
      ))}
      {hidden > 0 && (
        <ToggleChip
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded(true)}
        >
          +{hidden} more
        </ToggleChip>
      )}
      {expanded && items.length > limit && (
        <ToggleChip
          type="button"
          aria-expanded={expanded}
          onClick={() => setExpanded(false)}
        >
          Show less
        </ToggleChip>
      )}
    </ChipRow>
  );
}
