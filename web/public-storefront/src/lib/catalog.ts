import { unstable_noStore as noStore } from "next/cache";
import { cache } from "react";
import type { Service, StorefrontProfile } from "@/types";

// SSR fetches the catalog straight from the in-cluster upstream. That returns
// byte-identical JSON to the public /api/services.json (obol-skill-md serves
// the already-merged envelope; Traefik just routes to it), but without a WAN
// hairpin back through the tunnel — so the storefront's own render never
// depends on the tunnel being healthy. FQDN over the short .svc form to avoid
// search-domain resolution flakiness on cold DNS.
const SERVICES_URL =
  process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc.cluster.local:8080";

const CATALOG_FETCH_TIMEOUT_MS = 8_000;

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
  if (!data || typeof data !== "object") {
    return { ...DEFAULT_STOREFRONT, services: [] };
  }
  // Accept the legacy bare-array catalog as well as the envelope. Older
  // serviceoffer-controller images publish /api/services.json as a bare
  // services[] array; rejecting it here dropped every service on those
  // clusters (not just a stale render).
  if (Array.isArray(data)) {
    return { ...DEFAULT_STOREFRONT, services: data as Service[] };
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
    // Opt out of Next's fetch cache so a hit doesn't pin a stale (or empty,
    // cold-start) catalog for the request-memoized lifetime.
    noStore();
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
