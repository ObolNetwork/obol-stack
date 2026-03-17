'use client';

import { useState } from 'react';
import type { ChainOption } from '@/components/atoms/ChainSelector';

export function useAgentFilters() {
  const [x402Only, setX402Only] = useState(false);
  const [protocolFilter, setProtocolFilter] = useState<string | undefined>(
    undefined
  );
  const [chainFilter, setChainFilter] = useState<ChainOption | undefined>(
    'base'
  );

  // Single-select: clicking the active protocol deselects it
  const toggleProtocol = (p: string) => {
    setProtocolFilter(prev => (prev === p ? undefined : p));
  };

  const hasActiveFilters = x402Only || !!protocolFilter || !!chainFilter;

  return {
    x402Only,
    setX402Only,
    protocolFilter,
    toggleProtocol,
    chainFilter,
    setChainFilter,
    hasActiveFilters,
  };
}
