import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Obol Stack",
  description: "Decentralised infrastructure services via x402 micropayments",
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="font-sans antialiased min-h-screen">{children}</body>
    </html>
  );
}
