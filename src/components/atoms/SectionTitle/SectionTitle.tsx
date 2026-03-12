import React from 'react';
import { Text, styled } from '@obolnetwork/obol-ui';

export interface SectionTitleProps {
  children: React.ReactNode;
  className?: string;
}

const StyledSectionTitle = styled(Text, {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
});

export function SectionTitle({ children, className }: SectionTitleProps) {
  return (
    <StyledSectionTitle className={className}>{children}</StyledSectionTitle>
  );
}
