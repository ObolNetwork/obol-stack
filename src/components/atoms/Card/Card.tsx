import React from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { CSS } from '@obolnetwork/obol-ui/dist/stitches.config';

export interface CardProps {
  children: React.ReactNode;
  className?: string;
  title?: string;
  hover?: boolean;
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
  css,
}: CardProps) {
  return (
    <StyledCard hover={hover} className={className} css={css}>
      {title && <CardTitle as="h2">{title}</CardTitle>}
      {children}
    </StyledCard>
  );
}
