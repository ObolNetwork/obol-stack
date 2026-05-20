'use client';

import { Flex, styled } from '@obolnetwork/obol-ui';
import { ListBulletIcon, DashboardIcon } from '@radix-ui/react-icons';

import type { MarketplaceView } from '@/lib/marketplace/types';

const STORAGE_KEY = 'obol.marketplace.view';

export interface ViewToggleProps {
  value: MarketplaceView;
  onChange: (next: MarketplaceView) => void;
}

const Group = styled(Flex, {
  alignItems: 'center',
  gap: 0,
  padding: 2,
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg03',
});

const ToggleButton = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: 32,
  height: 28,
  borderRadius: '$1',
  border: 'none',
  background: 'transparent',
  color: '$body',
  cursor: 'pointer',
  transition: 'all 0.15s ease',

  '&:hover': {
    color: '$light',
  },

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        color: '$obolGreen',
      },
    },
  },
});

/**
 * Canonical two-icon view switcher.
 *
 *   [ ≡ list ]  [ ▦ cards ]
 *
 * Persists the choice to localStorage so the user lands on their preferred
 * view next session. `value` is still the source of truth — the caller owns
 * state; this component only emits onChange and writes through.
 */
export function ViewToggle({ value, onChange }: ViewToggleProps) {
  const select = (next: MarketplaceView) => {
    onChange(next);
    if (typeof window !== 'undefined') {
      try {
        window.localStorage.setItem(STORAGE_KEY, next);
      } catch {
        // localStorage disabled / quota — silently ignore; state still updates.
      }
    }
  };

  return (
    <Group role="group" aria-label="View">
      <ToggleButton
        type="button"
        active={value === 'list'}
        aria-pressed={value === 'list'}
        aria-label="List view"
        title="List view"
        onClick={() => select('list')}
      >
        <ListBulletIcon width={16} height={16} />
      </ToggleButton>
      <ToggleButton
        type="button"
        active={value === 'cards'}
        aria-pressed={value === 'cards'}
        aria-label="Cards view"
        title="Cards view"
        onClick={() => select('cards')}
      >
        <DashboardIcon width={16} height={16} />
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
