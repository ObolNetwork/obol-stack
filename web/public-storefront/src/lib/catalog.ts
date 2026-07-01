import { unstable_noStore as noStore } from "next/cache";
import { headers } from "next/headers";
import { cache } from "react";
import type { Service, StorefrontProfile } from "@/types";

const DEFAULT_UPSTREAM =
  process.env.SERVICES_URL ?? "http://obol-skill-md.x402.svc.cluster.local:8080";

const CATALOG_FETCH_TIMEOUT_MS = 8_000;

function upstreamBase(): string {
  return DEFAULT_UPSTREAM.replace(/\/$/, "");
}

function isPodAddress(host: string): boolean {
  const hostname = host.split(":")[0] ?? "";
  return /^\d+\.\d+\.\d+\.\d+$/.test(hostname);
}

// SSR should use the same /api/services.json URL the browser hits (Traefik →
// obol-skill-md). Probes and cold starts often arrive with a pod IP Host
// header — fall back to the in-cluster upstream in that case.
async function resolvePublicCatalogURL(): Promise<string | null> {
  try {
    const h = await headers();
    const host = h.get("x-forwarded-host") ?? h.get("host");
    if (!host || isPodAddress(host)) {
      return null;
    }
    const proto =
      h.get("x-forwarded-proto") ??
      (host.includes("localhost") || host.startsWith("obol.stack")
        ? "http"
        : "https");
    return `${proto}://${host}/api/services.json`;
  } catch {
    return null;
  }
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

function unwrapServices(data: unknown): Service[] {
  if (Array.isArray(data)) {
    return data as Service[];
  }
  if (data && typeof data === "object") {
    const services = (data as { services?: unknown }).services;
    if (Array.isArray(services)) {
      return services as Service[];
    }
  }
  return [];
}

function parseCatalogDocument(data: unknown): ServiceCatalogDocument {
  if (!data || typeof data !== "object") {
    return { ...DEFAULT_STOREFRONT, services: [] };
  }
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

async function fetchCatalogDocumentOnce(
  url: string,
): Promise<ServiceCatalogDocument> {
  const res = await fetch(url, {
    cache: "no-store",
    signal: AbortSignal.timeout(CATALOG_FETCH_TIMEOUT_MS),
  });
  if (!res.ok) {
    throw new Error(`catalog ${res.status} ${res.statusText} from ${url}`);
  }
  return parseCatalogDocument(await res.json());
}

export const fetchCatalogDocument = cache(
  async (): Promise<ServiceCatalogDocument> => {
    noStore();
    const upstream = `${upstreamBase()}/api/services.json`;
    const publicURL = await resolvePublicCatalogURL();
    const urls =
      publicURL && publicURL !== upstream ? [publicURL, upstream] : [upstream];

    for (const url of urls) {
      try {
        return await fetchCatalogDocumentOnce(url);
      } catch (err) {
        console.error("[storefront] catalog fetch failed:", url, err);
      }
    }
    return { ...DEFAULT_STOREFRONT, services: [] };
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
