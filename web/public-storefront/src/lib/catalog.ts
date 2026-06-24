import { cache } from "react";
import type { Service, StorefrontProfile } from "@/types";

const SERVICES_URL =
  process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc:8080";

const CATALOG_FETCH_TIMEOUT_MS = 5_000;

async function fetchCatalog(path: string): Promise<Response> {
  return fetch(`${SERVICES_URL}${path}`, {
    cache: "no-store",
    signal: AbortSignal.timeout(CATALOG_FETCH_TIMEOUT_MS),
  });
}

export const DEFAULT_LOGO_PATH = "/obol-stack-logo.png";

export const DEFAULT_STOREFRONT: StorefrontProfile = {
  displayName: "Obol Stack",
  tagline: "Unlock Agent and API services with digital payments.",
  logoUrl: DEFAULT_LOGO_PATH,
};

/** True when the logo is the stack default (relative or tunnel-absolute URL). */
export function isDefaultStorefrontLogo(logoUrl: string): boolean {
  return (
    logoUrl === DEFAULT_LOGO_PATH || logoUrl.endsWith(DEFAULT_LOGO_PATH)
  );
}

export const DEFAULT_HERO_TITLE = "Agent services";

export const fetchServices = cache(async (): Promise<Service[]> => {
  try {
    const res = await fetchCatalog("/api/services.json");
    if (!res.ok) return [];
    return res.json();
  } catch {
    return [];
  }
});

export const fetchStorefront = cache(async (): Promise<StorefrontProfile> => {
  try {
    const res = await fetchCatalog("/api/storefront.json");
    if (!res.ok) return DEFAULT_STOREFRONT;
    const data = (await res.json()) as Partial<StorefrontProfile>;
    return {
      displayName: data.displayName || DEFAULT_STOREFRONT.displayName,
      tagline: data.tagline || DEFAULT_STOREFRONT.tagline,
      logoUrl: data.logoUrl || DEFAULT_STOREFRONT.logoUrl,
    };
  } catch {
    return DEFAULT_STOREFRONT;
  }
});
