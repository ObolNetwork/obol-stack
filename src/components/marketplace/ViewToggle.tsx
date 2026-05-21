'use client';

import { Flex, styled } from '@obolnetwork/obol-ui';

import type { MarketplaceView } from '@/lib/marketplace/types';

const STORAGE_KEY = 'obol.marketplace.view';

export interface ViewToggleProps {
  value: MarketplaceView;
  onChange: (next: MarketplaceView) => void;
}

const Group = styled(Flex, {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 2,
  padding: 3,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
});

const ToggleButton = styled('button', {
  width: 30,
  height: 30,
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  borderRadius: '$1',
  border: '1px solid transparent',
  background: 'transparent',
  color: '$body',
  cursor: 'pointer',
  transition: 'background 120ms, color 120ms, border-color 120ms',
  padding: 0,

  '&:hover': { color: '$light' },

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

// Custom SVGs from the Services design canvas. The list glyph is the
// standard "three dots + lines" indicator; the grid glyph is a 2x2 of
// rounded-square outlines. Both use currentColor so the active variant's
// $obolGreen takes effect automatically.
const ListIcon = () => (
  <svg
    width="16"
    height="16"
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
  >
    <circle cx="3" cy="4" r="1.1" fill="currentColor" />
    <circle cx="3" cy="8" r="1.1" fill="currentColor" />
    <circle cx="3" cy="12" r="1.1" fill="currentColor" />
    <path
      d="M6.5 4h7M6.5 8h7M6.5 12h7"
      stroke="currentColor"
      strokeWidth="1.4"
      strokeLinecap="round"
    />
  </svg>
);

const GridIcon = () => (
  <svg
    width="16"
    height="16"
    viewBox="0 0 16 16"
    fill="none"
    aria-hidden="true"
  >
    <rect
      x="2.25"
      y="2.25"
      width="4.5"
      height="4.5"
      rx="1"
      stroke="currentColor"
      strokeWidth="1.4"
    />
    <rect
      x="9.25"
      y="2.25"
      width="4.5"
      height="4.5"
      rx="1"
      stroke="currentColor"
      strokeWidth="1.4"
    />
    <rect
      x="2.25"
      y="9.25"
      width="4.5"
      height="4.5"
      rx="1"
      stroke="currentColor"
      strokeWidth="1.4"
    />
    <rect
      x="9.25"
      y="9.25"
      width="4.5"
      height="4.5"
      rx="1"
      stroke="currentColor"
      strokeWidth="1.4"
    />
  </svg>
);

/**
 * Canonical two-icon view switcher.
 *
 *   [ ≡ list ]  [ ▦ cards ]
 *
 * Persists the choice to localStorage so the user lands on their preferred
 * view next session.
 */
export function ViewToggle({ value, onChange }: ViewToggleProps) {
  const select = (next: MarketplaceView) => {
    onChange(next);
    if (typeof window !== 'undefined') {
      try {
        window.localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // localStorage disabled / quota — silently ignore.
      }
    }
  };

  return (
    <Group role="tablist" aria-label="View">
      <ToggleButton
        type="button"
        role="tab"
        active={value === 'list'}
        aria-selected={value === 'list'}
        aria-label="List view"
        title="List view"
        onClick={() => select('list')}
      >
        <ListIcon />
      </ToggleButton>
      <ToggleButton
        type="button"
        role="tab"
        active={value === 'cards'}
        aria-selected={value === 'cards'}
        aria-label="Cards view"
        title="Cards view"
        onClick={() => select('cards')}
      >
        <GridIcon />
      </ToggleButton>
    </Group>
  );
}

export function readStoredView(
  fallback: MarketplaceView = 'cards'
): MarketplaceView {
  if (typeof window === 'undefined') return fallback;
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === 'list' || stored === 'cards') return stored;
  } catch {
    // ignore
  }
  return fallback;
}
