'use client';

import { Flex, styled } from '@obolnetwork/obol-ui';
import { Button } from '../../atoms/Button/Button';

export type TopNavId = 'home' | 'marketplace' | 'agents';

export interface TopNavItem {
  id: TopNavId;
  label: string;
  badge?: string;
}

export const TOP_NAV_ITEMS: readonly TopNavItem[] = [
  { id: 'home', label: 'Home' },
  { id: 'marketplace', label: 'Marketplace', badge: 'NEW' },
  // Agents is hidden for now — restore this entry to bring it back.
  // The 'agents' id, route helpers, and API routes remain intact.
  // { id: 'agents', label: 'Agents' },
] as const;

const Nav = styled('nav', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xxxs',
});

const NavItem = styled(Button, {
  variants: {
    active: {
      true: {
        background: '$bg03',
        color: '$light',
      },
    },
  },
});

const NewChip = styled('span', {
  fontSize: 9,
  fontWeight: '$bold',
  letterSpacing: '0.06em',
  padding: '2px 6px',
  borderRadius: '$pill',
  background: 'rgba(47, 228, 171, 0.13)',
  color: '$obolGreen',
  textTransform: 'uppercase',
  lineHeight: 1,
});

export interface TopNavProps {
  active?: TopNavId;
  onSelect?: (id: TopNavId) => void;
}

export function TopNav({ active = 'home', onSelect }: TopNavProps) {
  return (
    <Nav aria-label="Primary">
      <Flex css={{ gap: '$xxxs', alignItems: 'center' }}>
        {TOP_NAV_ITEMS.map(item => {
          const isActive = item.id === active;
          return (
            <NavItem
              key={item.id}
              type="button"
              variant="bare"
              size="md"
              pill
              active={isActive}
              aria-current={isActive ? 'page' : undefined}
              onClick={() => onSelect?.(item.id)}
            >
              {item.label}
              {item.badge && <NewChip>{item.badge}</NewChip>}
            </NavItem>
          );
        })}
      </Flex>
    </Nav>
  );
}
