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

export interface ServiceCatalogDocument extends StorefrontProfile {
  services: Service[];
}

function parseCatalogDocument(data: unknown): ServiceCatalogDocument {
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    return { ...DEFAULT_STOREFRONT, services: [] };
  }
  const doc = data as Partial<ServiceCatalogDocument>;
  return {
    displayName: doc.displayName || DEFAULT_STOREFRONT.displayName,
    tagline: doc.tagline || DEFAULT_STOREFRONT.tagline,
    logoUrl: doc.logoUrl || DEFAULT_STOREFRONT.logoUrl,
    services: Array.isArray(doc.services) ? doc.services : [],
  };
}

export const fetchCatalogDocument = cache(
  async (): Promise<ServiceCatalogDocument> => {
    try {
      const res = await fetchCatalog("/api/services.json");
      if (!res.ok) return { ...DEFAULT_STOREFRONT, services: [] };
      return parseCatalogDocument(await res.json());
    } catch {
      return { ...DEFAULT_STOREFRONT, services: [] };
    }
  },
);

export const fetchServices = cache(async (): Promise<Service[]> => {
  const catalog = await fetchCatalogDocument();
  return catalog.services;
});

export const fetchStorefront = cache(async (): Promise<StorefrontProfile> => {
  const catalog = await fetchCatalogDocument();
  return {
    displayName: catalog.displayName,
    tagline: catalog.tagline,
    logoUrl: catalog.logoUrl,
  };
});
