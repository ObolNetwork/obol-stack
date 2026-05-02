import type { Metadata } from "next";
import { DM_Sans } from "next/font/google";
import "./globals.css";

const dmSans = DM_Sans({
  subsets: ["latin"],
  display: "swap",
  weight: ["400", "500", "600", "700"],
  variable: "--font-dm-sans",
});

export const metadata: Metadata = {
  title: "Obol Stack — Agent services",
  description:
    "Decentralised infrastructure services from this Obol Agent, available via x402 micropayments.",
  icons: {
    icon: "/favicon.png",
  },
  openGraph: {
    title: "Obol Stack — Agent services",
    description:
      "Decentralised infrastructure services via x402 micropayments.",
    images: ["/obol-stack-logo.png"],
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className={dmSans.variable}>
      <body className="font-sans antialiased min-h-screen">{children}</body>
    </html>
  );
}
