import type { Metadata, Viewport } from "next";
import { headers } from "next/headers";
import { DM_Sans } from "next/font/google";
import type { Service } from "@/types";
import "./globals.css";

const dmSans = DM_Sans({
  subsets: ["latin"],
  display: "swap",
  weight: ["400", "500", "600", "700"],
  variable: "--font-dm-sans",
});

const SERVICES_URL =
  process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc:8080";

const DEFAULT_TITLE = "Obol Stack — Buy agent services";
const DEFAULT_DESCRIPTION =
  "Purchase from specialised Agents and APIs with digital payments. On demand inference, agents, and HTTP services with OBOL and USDC support.";

// Derive the public site URL from the incoming request so OG/Twitter scrapers
// see the tunnel hostname they hit (rather than the in-cluster default). Falls
// back to NEXT_PUBLIC_SITE_URL, then the local-dev default.
async function resolveSiteUrl(): Promise<string> {
  try {
    const h = await headers();
    const forwardedHost = h.get("x-forwarded-host") ?? h.get("host");
    const forwardedProto =
      h.get("x-forwarded-proto") ??
      (forwardedHost?.includes("localhost") || forwardedHost === "obol.stack:8080"
        ? "http"
        : "https");
    if (forwardedHost) return `${forwardedProto}://${forwardedHost}`;
  } catch {
    // headers() unavailable (e.g. build-time prerender) — fall through.
  }
  return process.env.NEXT_PUBLIC_SITE_URL ?? "http://obol.stack:8080";
}

async function fetchServices(): Promise<Service[]> {
  try {
    const res = await fetch(`${SERVICES_URL}/api/services.json`, {
      cache: "no-store",
    });
    if (!res.ok) return [];
    return res.json();
  } catch {
    return [];
  }
}

function buildDynamicCopy(services: Service[]) {
  if (services.length === 0) {
    return { title: DEFAULT_TITLE, description: DEFAULT_DESCRIPTION };
  }
  const total = services.length;
  const summary = `${total} service${total === 1 ? "" : "s"}`;
  const title = `Obol Stack — ${summary} for sale`;
  const sample = services
    .slice(0, 3)
    .map((s) => s.model ?? s.name)
    .filter(Boolean)
    .join(", ");
  const description = sample
    ? `Buy agent services from this Obol Agent: ${sample}. Pay per call in USDC or OBOL on Base.`
    : DEFAULT_DESCRIPTION;
  return { title, description };
}

export async function generateMetadata(): Promise<Metadata> {
  const [services, siteUrl] = await Promise.all([
    fetchServices(),
    resolveSiteUrl(),
  ]);
  const { title, description } = buildDynamicCopy(services);

  return {
    metadataBase: new URL(siteUrl),
    title,
    description,
    applicationName: "Obol Stack",
    icons: {
      icon: [
        { url: "/icon-32.png", type: "image/png", sizes: "32x32" },
        { url: "/icon-16.png", type: "image/png", sizes: "16x16" },
        { url: "/favicon.png", type: "image/png", sizes: "256x256" },
      ],
      apple: [{ url: "/apple-icon.png", sizes: "180x180" }],
    },
    manifest: "/manifest.webmanifest",
    openGraph: {
      type: "website",
      siteName: "Obol Stack",
      title,
      description,
      url: siteUrl,
    },
    twitter: {
      card: "summary_large_image",
      title,
      description,
    },
    robots: {
      index: true,
      follow: true,
    },
  };
}

export const viewport: Viewport = {
  themeColor: "#091011",
  colorScheme: "dark",
};

function JsonLd({
  services,
  siteUrl,
}: {
  services: Service[];
  siteUrl: string;
}) {
  const data = {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: "Obol Stack",
    description: DEFAULT_DESCRIPTION,
    url: siteUrl,
    publisher: {
      "@type": "Organization",
      name: "Obol",
      url: "https://obol.org",
    },
    offers: services.map((s) => ({
      "@type": "Offer",
      name: s.name,
      description: s.description,
      url: `${siteUrl}${s.endpoint.startsWith("/") ? "" : "/"}${s.endpoint}`,
      price: s.priceRaw ?? s.price,
      priceCurrency: s.asset?.symbol ?? "USDC",
      eligibleTransactionVolume: {
        "@type": "PriceSpecification",
        price: s.price,
      },
      areaServed: s.network,
    })),
  };
  return (
    <script
      type="application/ld+json"
      dangerouslySetInnerHTML={{ __html: JSON.stringify(data) }}
    />
  );
}

export default async function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const [services, siteUrl] = await Promise.all([
    fetchServices(),
    resolveSiteUrl(),
  ]);
  return (
    <html lang="en" className={dmSans.variable}>
      <body className="font-sans antialiased min-h-screen">
        {children}
        <JsonLd services={services} siteUrl={siteUrl} />
      </body>
    </html>
  );
}
