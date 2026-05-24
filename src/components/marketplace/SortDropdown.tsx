'use client';

import type { ChangeEvent } from 'react';
import { styled } from '@obolnetwork/obol-ui';

import type { MarketplaceSort } from '@/lib/marketplace/types';

export interface SortDropdownProps {
  value: MarketplaceSort;
  onChange: (next: MarketplaceSort) => void;
}

const OPTIONS: { value: MarketplaceSort; label: string }[] = [
  { value: 'price_asc', label: 'Lowest price' },
  { value: 'price_desc', label: 'Highest price' },
  { value: 'name', label: 'Name (A–Z)' },
  { value: 'recent', label: 'Recent' },
];

// Inline chevron rendered as a background image so it sits inside the native
// <select>. SVG kept in URL form to avoid a separate icon import.
const CHEVRON = `url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 16 16' fill='none'><path d='M4 6l4 4 4-4' stroke='%23a8a8a3' stroke-width='1.6' stroke-linecap='round' stroke-linejoin='round'/></svg>")`;

const Select = styled('select', {
  appearance: 'none',
  WebkitAppearance: 'none',
  MozAppearance: 'none',
  height: 30,
  paddingLeft: '$xs',
  paddingRight: 26,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$light',
  fontSize: '$2',
  fontFamily: 'inherit',
  fontWeight: '$medium',
  cursor: 'pointer',
  lineHeight: 1,
  transition: 'background 120ms, border-color 120ms, color 120ms',
  backgroundImage: CHEVRON,
  backgroundRepeat: 'no-repeat',
  backgroundPosition: 'right 8px center',

  '&:hover': {
    background: '$bg04',
    borderColor: '$bg05',
  },
  '&:focus-visible': {
    outline: 'none',
    borderColor: '$obolGreen',
  },
});

/**
 * Sort selector used in the marketplace results bar. Native <select> for
 * free keyboard + mobile behavior, styled to match the design canvas's
 * "Lowest price ▾" trigger.
 */
export function SortDropdown({ value, onChange }: SortDropdownProps) {
  return (
    <Select
      aria-label="Sort services"
      value={value}
      onChange={(e: ChangeEvent<HTMLSelectElement>) =>
        onChange(e.target.value as MarketplaceSort)
      }
    >
      {OPTIONS.map(opt => (
        <option key={opt.value} value={opt.value}>
          {opt.label}
        </option>
      ))}
    </Select>
  );
}
