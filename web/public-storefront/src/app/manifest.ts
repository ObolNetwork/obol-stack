import type { MetadataRoute } from "next";
import { fetchStorefront, isDefaultStorefrontLogo } from "@/lib/catalog";

export default async function manifest(): Promise<MetadataRoute.Manifest> {
  const storefront = await fetchStorefront();
  const iconSrc = isDefaultStorefrontLogo(storefront.logoUrl)
    ? "/icon-192.png"
    : storefront.logoUrl;
  const largeIcon = isDefaultStorefrontLogo(storefront.logoUrl)
    ? "/icon-512.png"
    : storefront.logoUrl;
  return {
    name: storefront.displayName,
    short_name: storefront.displayName,
    description: storefront.tagline,
    start_url: "/",
    display: "standalone",
    background_color: "#091011",
    theme_color: "#091011",
    icons: [
      { src: iconSrc, sizes: "192x192", type: "image/png" },
      { src: largeIcon, sizes: "512x512", type: "image/png" },
      {
        src: largeIcon,
        sizes: "512x512",
        type: "image/png",
        purpose: "maskable",
      },
    ],
  };
}
