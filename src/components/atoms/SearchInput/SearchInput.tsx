import React from 'react';
import { styled } from '@obolnetwork/obol-ui';

export interface SearchInputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  className?: string;
}

const StyledSearchInput = styled('input', {
  backgroundColor: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  color: '$light',
  fontSize: '$2',
  padding: '6px 12px',
  outline: 'none',
  width: '200px',
  '&::placeholder': { color: '$muted' },
  '&:focus': { borderColor: '$obolGreen' },
});

export function SearchInput({ className, ...props }: SearchInputProps) {
  return <StyledSearchInput className={className} {...props} />;
}
