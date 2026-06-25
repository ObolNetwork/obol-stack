import { headers } from "next/headers";

function usesPlainHTTP(host: string | null): boolean {
  if (!host) return false;
  return /^(localhost|127\.|0\.0\.0\.0|\[::1\]|::1|obol\.stack)(:|$)/.test(
    host,
  );
}

// Derive the public site URL from the incoming request so metadata and
// generated images use the hostname that scrapers hit.
export async function resolveSiteUrl(): Promise<string> {
  try {
    const h = await headers();
    const forwardedHost = h.get("x-forwarded-host") ?? h.get("host");
    const forwardedProto =
      h.get("x-forwarded-proto") ??
      (usesPlainHTTP(forwardedHost) ? "http" : "https");
    if (forwardedHost) return `${forwardedProto}://${forwardedHost}`;
  } catch {
    // headers() unavailable (e.g. build-time prerender) — fall through.
  }
  return process.env.NEXT_PUBLIC_SITE_URL ?? "http://obol.stack:8080";
}

export function resolvePublicUrl(rawUrl: string, siteUrl: string): string {
  const value = rawUrl.trim();
  if (!value) return "";
  try {
    return new URL(value, siteUrl).toString();
  } catch {
    return "";
  }
}
