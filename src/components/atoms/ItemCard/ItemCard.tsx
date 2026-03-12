import React from 'react';
import { Box, styled } from '@obolnetwork/obol-ui';
import { CSS } from '@obolnetwork/obol-ui/dist/stitches.config';

export interface ItemCardProps {
  children: React.ReactNode;
  className?: string;
  css?: CSS;
}

const StyledItemCard = styled(Box, {
  padding: '$sm $md',
  borderRadius: '$2',
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  transition: 'border-color 0.2s ease',
  '&:hover': { borderColor: '$body' },
});

export function ItemCard({ children, className, css }: ItemCardProps) {
  return (
    <StyledItemCard className={className} css={css}>
      {children}
    </StyledItemCard>
  );
}
