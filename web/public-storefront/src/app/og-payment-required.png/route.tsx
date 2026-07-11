import { ImageResponse } from "next/og";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fetchStorefront, isDefaultStorefrontLogo } from "@/lib/catalog";
import { isDarkTheme, themeToken } from "@/lib/theme";
import { resolvePublicUrl, resolveSiteUrl } from "@/lib/site-url";

// OG image for HTTP 402 responses emitted by x402-verifier. Referenced from
// the verifier's HTML 402 body via absolute URL on the same tunnel host.
// Same Satori/JSX pipeline as src/app/opengraph-image.tsx so styling stays
// consistent across the two surfaces. Rendered per-request (branding +
// theme come from the live catalog) but edge/browser-cached for an hour.

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const SIZE = { width: 1200, height: 630 };

function logoDataUrl(file: string): string {
  const bytes = readFileSync(join(process.cwd(), "public", file));
  return `data:image/png;base64,${bytes.toString("base64")}`;
}

export async function GET() {
  const [storefront, siteUrl] = await Promise.all([
    fetchStorefront(),
    resolveSiteUrl(),
  ]);
  const vars = storefront.themeVars;
  const dark = isDarkTheme(storefront.theme);
  const textLight = themeToken("light", vars);
  const textBody = themeToken("body", vars);
  const accent = themeToken("green", vars);
  const bg = themeToken("bg01", vars);
  const panel = themeToken("bg02", vars);
  const stroke = themeToken("stroke", vars);

  const customLogoSrc = isDefaultStorefrontLogo(storefront.logoUrl)
    ? ""
    : resolvePublicUrl(storefront.logoUrl, siteUrl);

  const Chip = ({ label }: { label: string }) => (
    <div
      style={{
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        height: 50,
        paddingLeft: 22,
        paddingRight: 22,
        borderRadius: 25,
        background: panel,
        border: `1.5px solid ${stroke}`,
        color: accent,
        fontSize: 22,
        fontWeight: 600,
      }}
    >
      {label}
    </div>
  );

  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          padding: 80,
          background: bg,
        }}
      >
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          {/* Brand, top-left. The default wordmark is light-on-dark, so
              light themes use the dark square mark + name instead. */}
          {customLogoSrc === "" && dark ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={logoDataUrl("obol-stack-logo.png")}
              alt={storefront.displayName}
              width={322}
              height={56}
              style={{ width: 322, height: 56 }}
            />
          ) : (
            <div style={{ display: "flex", alignItems: "center", gap: 18 }}>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={
                  customLogoSrc === ""
                    ? logoDataUrl("obol-logo.png")
                    : customLogoSrc
                }
                alt={storefront.displayName}
                width={72}
                height={72}
                style={{
                  width: 72,
                  height: 72,
                  borderRadius: 16,
                  objectFit: "cover",
                }}
              />
              <div
                style={{
                  display: "flex",
                  color: textLight,
                  fontSize: 38,
                  fontWeight: 700,
                }}
              >
                {storefront.displayName}
              </div>
            </div>
          )}
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              height: 42,
              paddingLeft: 22,
              paddingRight: 22,
              borderRadius: 21,
              background: panel,
              border: `1.5px solid ${accent}`,
              color: accent,
              fontSize: 22,
              fontWeight: 600,
              fontFamily: "monospace",
            }}
          >
            HTTP 402
          </div>
        </div>

        <div
          style={{
            display: "flex",
            marginTop: 110,
            color: textLight,
            fontSize: 96,
            fontWeight: 700,
            letterSpacing: -2,
            lineHeight: 1,
          }}
        >
          Payment required
        </div>

        <div
          style={{
            display: "flex",
            marginTop: 28,
            color: textBody,
            fontSize: 34,
            fontWeight: 500,
            lineHeight: 1.3,
          }}
        >
          Unlock this service from {storefront.displayName}. Pay per call in
          USDC or OBOL.
        </div>

        <div
          style={{
            display: "flex",
            marginTop: "auto",
            gap: 16,
          }}
        >
          <Chip label="x402" />
          <Chip label="Base" />
          <Chip label="USDC" />
          <Chip label="OBOL" />
        </div>
      </div>
    ),
    {
      ...SIZE,
      headers: {
        "Content-Type": "image/png",
        "Cache-Control": "public, max-age=3600",
      },
    },
  );
}
