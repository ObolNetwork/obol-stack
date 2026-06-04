import { DM_Sans } from 'next/font/google';
import type { Metadata } from 'next';
import './globals.css';

const dmSans = DM_Sans({
  variable: '--font-dm-sans',
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  display: 'swap',
  preload: false, // Disable preloading to fix Turbopack issue
});

export const metadata: Metadata = {
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
  return (
    <html lang="en">
      <body className={dmSans.variable} style={{ backgroundColor: '#091011' }}>
        {children}
      </body>
    </html>
  );
}
