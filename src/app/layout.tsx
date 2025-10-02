'use client';
import { DM_Sans } from 'next/font/google';
import './globals.css';
import { CopilotKit } from '@copilotkit/react-core';
import '@copilotkit/react-ui/styles.css';

const dmSans = DM_Sans({
  variable: '--font-dm-sans',
  subsets: ['latin'],
  weight: ['400', '500', '600', '700'],
  display: 'swap',
  preload: false, // Disable preloading to fix Turbopack issue
});

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en">
      <body className={dmSans.variable}>
        <CopilotKit runtimeUrl="/api/copilotkit" agent="obol_agent">
          {children}
        </CopilotKit>
      </body>
    </html>
  );
}
