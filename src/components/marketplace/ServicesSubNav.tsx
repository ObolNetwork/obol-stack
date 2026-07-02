'use client';

import Link from 'next/link';
import type { MouseEventHandler } from 'react';
import { styled } from '@obolnetwork/obol-ui';

import { Button } from '@/components/atoms/Button';

export type ServicesTab = 'browse' | 'purchases' | 'listings';

export interface ServicesSubNavProps {
  active: ServicesTab;
  counts?: Partial<Record<ServicesTab, number | null>>;
  onNewListing?: MouseEventHandler<HTMLButtonElement>;
}

const TABS: Array<{ id: ServicesTab; label: string; href: string }> = [
  { id: 'browse', label: 'Browse', href: '/marketplace' },
  { id: 'purchases', label: 'My Purchases', href: '/marketplace/purchases' },
  { id: 'listings', label: 'My Listings', href: '/marketplace/listings' },
];

const Bar = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xs',
  flexWrap: 'wrap',
  marginBottom: '$lg',
});

const Spacer = styled('span', { flex: 1 });

// SubNav chip — matches the design canvas's FilterChip
// (fs-2 / 6px 12px / pill radius). Renders as either an <a> (next/link)
// for the active/inactive Browse and links, or a <button> for inert tabs.
const chipReset = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  padding: '6px 12px',
  borderRadius: '$pill',
  border: '1px solid $stroke',
  background: '$bg03',
  color: '$body',
  fontFamily: 'inherit',
  fontSize: '$2',
  fontWeight: '$medium',
  lineHeight: 1,
  cursor: 'pointer',
  textDecoration: 'none',
  transition: 'background 150ms, color 150ms, border-color 150ms',
  '&:hover': { color: '$light' },
  // Match the Button atom's focus ring — links deserve it too.
  '&:focus-visible': {
    outline: '2px solid $obolGreen',
    outlineOffset: 2,
  },
} as const;

const ChipLink = styled(Link, {
  ...chipReset,

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
      },
    },
  },
});

const Count = styled('span', {
  fontFamily: 'monospace',
  fontSize: '$1',
  opacity: 0.55,

  variants: {
    active: {
      true: { opacity: 0.75 },
    },
  },
});

function PlusIcon() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M8 3v10M3 8h10"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
      />
    </svg>
  );
}

export function ServicesSubNav({
  active,
  counts,
  onNewListing,
}: ServicesSubNavProps) {
  return (
    <Bar>
      {TABS.map(t => {
        const isActive = t.id === active;
        const c = counts?.[t.id];
        return (
          <ChipLink
            key={t.id}
            href={t.href}
            active={isActive}
            aria-current={isActive ? 'page' : undefined}
          >
            {t.label}
            {c !== undefined && c !== null ? (
              <Count active={isActive}>{c}</Count>
            ) : null}
          </ChipLink>
        );
      })}
      <Spacer />
      <Button
        type="button"
        variant="primary"
        size="sm"
        css={{ flexShrink: 0 }}
        onClick={onNewListing}
        disabled={!onNewListing}
        title={onNewListing ? 'Create a new listing' : 'Coming soon'}
      >
        <PlusIcon />
        New listing
      </Button>
    </Bar>
  );
}
