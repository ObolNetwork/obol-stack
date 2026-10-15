import type { Metadata } from 'next';
import type { ReactNode } from 'react';
import { DM_Mono } from 'next/font/google';

import { StorefrontShell } from './StorefrontShell';
import '../../components/storefront/storefront.css';

const dmMono = DM_Mono({
  variable: '--font-dm-mono',
  subsets: ['latin'],
  weight: ['400', '500'],
  display: 'swap',
  preload: false,
});

export const metadata: Metadata = {
  title: 'Storefront branding · Obol Stack',
};

/**
 * /storefront is the operator-only branding editor (served on obol.stack),
 * not the public tunnel storefront — so it gets the same dashboard shell as
 * every other operator route. The editor body keeps its light "shop" look
 * on purpose: it previews what buyers will see.
 */
export default function StorefrontLayout({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <StorefrontShell fontClassName={dmMono.variable}>
      {children}
    </StorefrontShell>
  );
}
