import React from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { CSS } from '@obolnetwork/obol-ui/dist/stitches.config';

export interface CardProps {
  children: React.ReactNode;
  className?: string;
  title?: string;
  hover?: boolean;
  section?: boolean;
  /**
   * Tier 2 "nested" card — for cards rendered inside a section card:
   * $bg03 fill, radius $2, padding $md. Default (tier 1) section cards
   * stay $bg02 / radius $4 with $lg padding at the call site.
   */
  nested?: boolean;
  css?: CSS;
}

const StyledCard = styled(Box, {
  borderRadius: '$4',
  backgroundColor: '$bg02',
  border: '1px solid $stroke',
  transition: 'background-color 300ms ease',

  variants: {
    hover: {
      true: {
        '&:hover': {
          backgroundColor: '$bg03',
        },
      },
    },
    section: {
      true: {
        position: 'relative',
        overflow: 'hidden',
        padding: '0',
      },
    },
    nested: {
      true: {
        borderRadius: '$2',
        backgroundColor: '$bg03',
        padding: '$md',
      },
    },
  },
});

const CardTitle = styled(Text, {
  fontWeight: '$semibold',
  color: '$light',
  marginBottom: '$md',
});

export function Card({
  children,
  className,
  title,
  hover = false,
  section = false,
  nested = false,
  css,
}: CardProps) {
  return (
    <StyledCard
      hover={hover}
      section={section}
      nested={nested}
      className={className}
      css={css}
    >
      {title && <CardTitle as="h2">{title}</CardTitle>}
      {children}
    </StyledCard>
  );
}
