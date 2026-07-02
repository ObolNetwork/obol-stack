'use client';

import { useState } from 'react';
import { Flex, Text, styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';

const ChipRow = styled(Flex, {
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: '$xxs',
});

const Chip = styled(Text, {
  fontSize: '$1',
  color: '$body',
  backgroundColor: tones.neutral.bg,
  border: `1px solid ${tones.neutral.border}`,
  borderRadius: '4px',
  padding: '1px 6px',
  lineHeight: 1.4,
  whiteSpace: 'nowrap',
});

// Success-toned expand/collapse chip on the canonical Button atom.
const ToggleChip = styled(Button, {
  background: tones.success.bg,
  borderColor: tones.success.border,
  color: tones.success.text,

  '&:hover:not(:disabled)': {
    background: tones.success.bg,
    borderColor: tones.success.text,
    color: tones.success.text,
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
          variant="ghost"
          size="sm"
          pill
          aria-expanded={expanded}
          onClick={() => setExpanded(true)}
        >
          +{hidden} more
        </ToggleChip>
      )}
      {expanded && items.length > limit && (
        <ToggleChip
          type="button"
          variant="ghost"
          size="sm"
          pill
          aria-expanded={expanded}
          onClick={() => setExpanded(false)}
        >
          Show less
        </ToggleChip>
      )}
    </ChipRow>
  );
}
