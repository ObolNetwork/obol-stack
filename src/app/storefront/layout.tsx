'use client';

import type { ReactNode } from 'react';
import { DM_Mono } from 'next/font/google';
import { Providers } from '../providers';
import '../../components/storefront/storefront.css';

const dmMono = DM_Mono({
  variable: '--font-dm-mono',
  subsets: ['latin'],
  weight: ['400', '500'],
  display: 'swap',
  preload: false,
});

export default function StorefrontLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <Providers>
      <div className={dmMono.variable}>{children}</div>
    </Providers>
  );
}
