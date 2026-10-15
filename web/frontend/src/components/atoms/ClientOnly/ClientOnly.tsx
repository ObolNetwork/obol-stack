'use client';

import { useSyncExternalStore, type ReactNode } from 'react';

const subscribe = () => () => {};

/** True after hydration, false during SSR/prerender and the hydration pass. */
export function useHasMounted(): boolean {
  return useSyncExternalStore(
    subscribe,
    () => true,
    () => false
  );
}

/**
 * Renders `children` only in the browser, after hydration. Use it to keep
 * wallet-dependent UI (wagmi/RainbowKit state, injected connectors) out of
 * the server render while the surrounding shell still server-renders.
 * `fallback` should reserve roughly the same space to avoid layout shift.
 */
export function ClientOnly({
  children,
  fallback = null,
}: {
  children: ReactNode;
  fallback?: ReactNode;
}) {
  return useHasMounted() ? <>{children}</> : <>{fallback}</>;
}
