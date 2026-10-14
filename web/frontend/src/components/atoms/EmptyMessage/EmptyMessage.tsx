import React from 'react';
import { Text, styled } from '@obolnetwork/obol-ui';

export interface EmptyMessageProps {
  children: React.ReactNode;
  className?: string;
}

const StyledEmptyMessage = styled(Text, {
  color: '$body',
  fontSize: '$3',
  textAlign: 'center',
  padding: '$lg 0',
});

export function EmptyMessage({ children, className }: EmptyMessageProps) {
  return (
    <StyledEmptyMessage className={className}>{children}</StyledEmptyMessage>
  );
}
