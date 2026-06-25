import { ImageResponse } from "next/og";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fetchStorefront, isDefaultStorefrontLogo } from "@/lib/catalog";
import { resolvePublicUrl, resolveSiteUrl } from "@/lib/site-url";

export const runtime = "nodejs";
export const alt = "Obol Stack — buy agent services";
export const size = { width: 1200, height: 630 };
export const contentType = "image/png";

const TEXT_LIGHT = "#DFEAED";
const TEXT_BODY = "#9CC2C9";
const OBOL_GREEN = "#2FE4AB";
const BG01 = "#091011";
const BG_PANEL = "#111F22";
const STROKE_GREEN = "#1D5249";

export default async function OpengraphImage() {
  const [storefront, siteUrl] = await Promise.all([
    fetchStorefront(),
    resolveSiteUrl(),
  ]);
  const wordmark = readFileSync(
    join(process.cwd(), "public", "obol-stack-logo.png"),
  );
  const wordmarkDataUrl = `data:image/png;base64,${wordmark.toString("base64")}`;
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
        background: BG_PANEL,
        border: `1.5px solid ${STROKE_GREEN}`,
        color: OBOL_GREEN,
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
          background: BG01,
        }}
      >
        {/* Brand, top-left */}
        {customLogoSrc === "" ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={wordmarkDataUrl}
            alt={storefront.displayName}
            width={322}
            height={56}
            style={{ width: 322, height: 56 }}
          />
        ) : (
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 18,
            }}
          >
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={customLogoSrc}
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
                color: TEXT_LIGHT,
                fontSize: 38,
                fontWeight: 700,
              }}
            >
              {storefront.displayName}
            </div>
          </div>
        )}

        {/* Headline */}
        <div
          style={{
            display: "flex",
            marginTop: 140,
            color: TEXT_LIGHT,
            fontSize: 96,
            fontWeight: 700,
            letterSpacing: -2,
            lineHeight: 1,
          }}
        >
          {storefront.displayName}
        </div>

        {/* Subtext */}
        <div
          style={{
            display: "flex",
            marginTop: 28,
            color: TEXT_BODY,
            fontSize: 34,
            fontWeight: 500,
            lineHeight: 1.3,
          }}
        >
          {storefront.tagline}
        </div>

        {/* Chips, bottom-left */}
        <div
          style={{
            display: "flex",
            marginTop: "auto",
            gap: 16,
          }}
        >
          <Chip label="Ethereum" />
          <Chip label="OBOL" />
          <Chip label="Base" />
          <Chip label="USDC" />
        </div>
      </div>
    ),
    { ...size },
  );
}
