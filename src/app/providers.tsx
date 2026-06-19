'use client';

import * as React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { WagmiProvider } from 'wagmi';
import { RainbowKitProvider, darkTheme } from '@rainbow-me/rainbowkit';
import { theme } from '@obolnetwork/obol-ui';
import { config } from '../lib/config/wagmi';

import '@rainbow-me/rainbowkit/styles.css';
import { LocalDevHostBanner } from '@/components/molecules/LocalDevHostBanner/LocalDevHostBanner';

const queryClient = new QueryClient();

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <WagmiProvider config={config}>
      <QueryClientProvider client={queryClient}>
        <RainbowKitProvider
          modalSize="compact"
          theme={darkTheme({
            accentColor: theme.colors.obolGreen.value,
            accentColorForeground: theme.colors.light.value,
            borderRadius: 'medium',
            fontStack: 'system',
            overlayBlur: 'small',
          })}
        >
          <LocalDevHostBanner />
          {children}
        </RainbowKitProvider>
      </QueryClientProvider>
    </WagmiProvider>
  );
}
