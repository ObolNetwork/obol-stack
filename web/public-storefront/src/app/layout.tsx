import type { Metadata, Viewport } from "next";
import { DM_Sans } from "next/font/google";
import type { Service } from "@/types";
import {
  fetchServices,
  fetchStorefront,
  isDefaultStorefrontLogo,
} from "@/lib/catalog";
import { isDarkTheme, safeCustomCss, themeStyle, themeToken } from "@/lib/theme";
import { resolveSiteUrl } from "@/lib/site-url";
import "./globals.css";

const dmSans = DM_Sans({
  subsets: ["latin"],
  display: "swap",
  weight: ["400", "500", "600", "700"],
  variable: "--font-dm-sans",
});

const DEFAULT_TITLE_SUFFIX = "Buy agent services";

function buildDynamicCopy(
  storefrontName: string,
  tagline: string,
  services: Service[],
) {
  if (services.length === 0) {
    return {
      title: `${storefrontName} — ${DEFAULT_TITLE_SUFFIX}`,
      description: tagline,
    };
  }
  const total = services.length;
  const summary = `${total} service${total === 1 ? "" : "s"}`;
  const title = `${storefrontName} — ${summary} for sale`;
  const sample = services
    .slice(0, 3)
    .map((s) => s.model ?? s.name)
    .filter(Boolean)
    .join(", ");
  const description = sample ? `${tagline} ${sample}.` : tagline;
  return { title, description };
}

function buildIcons(storefront: Awaited<ReturnType<typeof fetchStorefront>>) {
  if (storefront.faviconUrl) {
    return {
      icon: [{ url: storefront.faviconUrl }],
      apple: [{ url: storefront.faviconUrl, sizes: "180x180" }],
    };
  }
  if (isDefaultStorefrontLogo(storefront.logoUrl)) {
    return {
      icon: [
        { url: "/icon-32.png", type: "image/png", sizes: "32x32" },
        { url: "/icon-16.png", type: "image/png", sizes: "16x16" },
        { url: "/favicon.png", type: "image/png", sizes: "256x256" },
      ],
      apple: [{ url: "/apple-icon.png", sizes: "180x180" }],
    };
  }
  return {
    icon: [{ url: storefront.logoUrl }],
    apple: [{ url: storefront.logoUrl, sizes: "180x180" }],
  };
}

export async function generateMetadata(): Promise<Metadata> {
  const [services, storefront, siteUrl] = await Promise.all([
    fetchServices(),
    fetchStorefront(),
    resolveSiteUrl(),
  ]);
  const { title, description } = buildDynamicCopy(
    storefront.displayName,
    storefront.tagline,
    services,
  );

  return {
    metadataBase: new URL(siteUrl),
    title,
    description,
    applicationName: storefront.displayName,
    icons: buildIcons(storefront),
    manifest: "/manifest.webmanifest",
    openGraph: {
      type: "website",
      siteName: storefront.displayName,
      title,
      description,
      url: siteUrl,
      // An operator-supplied preview image wins; otherwise Next falls back
      // to the generated opengraph-image route.
      ...(storefront.ogImageUrl ? { images: [storefront.ogImageUrl] } : {}),
    },
    twitter: {
      card: "summary_large_image",
      title,
      description,
      // Keep link-preview parity with Open Graph when the operator sets ogImageUrl.
      ...(storefront.ogImageUrl ? { images: [storefront.ogImageUrl] } : {}),
    },
    robots: {
      index: true,
      follow: true,
    },
  };
}

export async function generateViewport(): Promise<Viewport> {
  const storefront = await fetchStorefront();
  return {
    themeColor: themeToken("bg01", storefront.themeVars),
    colorScheme: isDarkTheme(storefront.theme) ? "dark" : "light",
  };
}

function JsonLd({
  services,
  siteUrl,
  storefrontName,
  storefrontTagline,
}: {
  services: Service[];
  siteUrl: string;
  storefrontName: string;
  storefrontTagline: string;
}) {
  const data = {
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: storefrontName,
    description: storefrontTagline,
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
  const [services, storefront, siteUrl] = await Promise.all([
    fetchServices(),
    fetchStorefront(),
    resolveSiteUrl(),
  ]);
  const customCss = safeCustomCss(storefront.customCss);
  return (
    <html
      lang="en"
      className={dmSans.variable}
      style={{
        ...themeStyle(storefront.themeVars),
        colorScheme: isDarkTheme(storefront.theme) ? "dark" : "light",
      }}
    >
      <body className="font-sans antialiased min-h-screen">
        {/* Operator stylesheet (sell info set --css-file). Guarded by
            safeCustomCss — the single Go-side validator is the primary
            gate; this re-check keeps a hostile catalog from breaking out
            of the style element. */}
        {customCss ? (
          <style
            data-obol="custom-css"
            dangerouslySetInnerHTML={{ __html: customCss }}
          />
        ) : null}
        {children}
        <JsonLd
          services={services}
          siteUrl={siteUrl}
          storefrontName={storefront.displayName}
          storefrontTagline={storefront.tagline}
        />
      </body>
    </html>
  );
}
