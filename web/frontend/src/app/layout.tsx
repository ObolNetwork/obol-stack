import { DM_Sans } from 'next/font/google';
import type { Metadata } from 'next';
import { StitchesRegistry } from './StitchesRegistry';

const dmSans = DM_Sans({
  variable: '--font-dm-sans',
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  display: 'swap',
  preload: false, // Disable preloading to fix Turbopack issue
});

export const metadata: Metadata = {
  title: 'Obol Stack',
  robots: {
    index: false,
    follow: false,
    googleBot: {
      index: false,
      follow: false,
    },
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  // Body background/colour come from the Stitches globals in
  // src/lib/ui/theme.ts ($bg01), flushed server-side by StitchesRegistry.
  return (
    <html lang="en">
      <body className={dmSans.variable}>
        <StitchesRegistry>{children}</StitchesRegistry>
      </body>
    </html>
  );
}
