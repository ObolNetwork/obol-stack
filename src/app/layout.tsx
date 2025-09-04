import type { Metadata } from 'next';
import { DM_Sans } from 'next/font/google';
import './globals.css';

const dmSans = DM_Sans({
  variable: '--font-dm-sans',
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
});

export const metadata: Metadata = {
  title: 'Obol Stack Dashboard',
  description: 'Monitoring dashboard for Obol Stack and Ethereum node status',
  icons: {
    icon: [
      { url: '/favicon.png', sizes: '256x256', type: 'image/png' },
      { url: '/obol-logo.png', sizes: '256x256', type: 'image/png' },
    ],
    apple: [{ url: '/obol-logo.png', sizes: '256x256', type: 'image/png' }],
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body
        className={`${dmSans.variable} font-sans antialiased bg-[radial-gradient(ellipse_at_top_left,rgba(47,228,171,0.08)_0%,transparent_60%)] text-zinc-200`}
      >
        {children}
      </body>
    </html>
  );
}
