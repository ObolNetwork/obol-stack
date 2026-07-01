import type { Service } from "@/types";

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

/**
 * Browser-side catalog fetch (Traefik / Next rewrite -> obol-skill-md).
 * Accepts both the envelope ({services:[...]}) and the legacy bare-array
 * catalog so a client refresh never drops services on older controllers.
 */
export async function fetchPublicCatalog(): Promise<Service[]> {
  const res = await fetch("/api/services.json", { cache: "no-store" });
  if (!res.ok) {
    throw new Error(`catalog ${res.status} ${res.statusText}`);
  }
  const data: unknown = await res.json();
  return unwrapServices(data);
}
