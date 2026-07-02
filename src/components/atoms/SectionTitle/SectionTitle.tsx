import React from 'react';
import { Text, styled } from '@obolnetwork/obol-ui';

export interface SectionTitleProps {
  children: React.ReactNode;
  className?: string;
  /** 1 = page section (default), 2 = subsection within a card. */
  level?: 1 | 2;
}

const StyledSectionTitle = styled(Text, {
  // Rendered as h2/h3 for document outline — strip UA heading margins.
  margin: 0,
  fontWeight: '$semibold',
  color: '$light',

  variants: {
    level: {
      1: { fontSize: '$5' },
      2: { fontSize: '$3' },
    },
  },

  defaultVariants: {
    level: 1,
  },
});

export function SectionTitle({
  children,
  className,
  level = 1,
}: SectionTitleProps) {
  return (
    <StyledSectionTitle
      as={level === 1 ? 'h2' : 'h3'}
      level={level}
      className={className}
    >
      {children}
    </StyledSectionTitle>
  );
}
