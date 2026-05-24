'use client';

import { useTopNav } from '@/hooks/useTopNav';
import { DashboardHeader, type DashboardHeaderProps } from './DashboardHeader';

/**
 * Thin client wrapper that wires DashboardHeader's TopNav to next/navigation.
 *
 * `activeNav` falls out of `usePathname()` so the highlighted tab always
 * matches the current route — no callsite has to remember to pass it. Any
 * prop the consumer passes wins over the routed default, which keeps this
 * usable from stories and tests that want to pin a state.
 */
export function NavigatedDashboardHeader(props: DashboardHeaderProps = {}) {
  const routed = useTopNav();
  return (
    <DashboardHeader
      activeNav={props.activeNav ?? routed.activeNav}
      onNavSelect={props.onNavSelect ?? routed.onNavSelect}
    />
  );
}
