import { ImageResponse } from "next/og";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// Static OG image for HTTP 402 responses emitted by x402-verifier. Referenced
// from the verifier's HTML 402 body via absolute URL on the same tunnel host.
// Same Satori/JSX pipeline as src/app/opengraph-image.tsx so styling stays
// consistent across the two surfaces.

export const runtime = "nodejs";
export const dynamic = "force-static";

const TEXT_LIGHT = "#DFEAED";
const TEXT_BODY = "#9CC2C9";
const OBOL_GREEN = "#2FE4AB";
const BG01 = "#091011";
const BG_PANEL = "#111F22";
const STROKE_GREEN = "#1D5249";
const SIZE = { width: 1200, height: 630 };

export async function GET() {
  const wordmark = readFileSync(
    join(process.cwd(), "public", "obol-stack-logo.png"),
  );
  const wordmarkDataUrl = `data:image/png;base64,${wordmark.toString("base64")}`;

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
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
          }}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={wordmarkDataUrl}
            alt="Obol Stack"
            width={322}
            height={56}
            style={{ width: 322, height: 56 }}
          />
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "center",
              height: 42,
              paddingLeft: 22,
              paddingRight: 22,
              borderRadius: 21,
              background: BG_PANEL,
              border: `1.5px solid ${OBOL_GREEN}`,
              color: OBOL_GREEN,
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
            color: TEXT_LIGHT,
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
            color: TEXT_BODY,
            fontSize: 34,
            fontWeight: 500,
            lineHeight: 1.3,
          }}
        >
          Unlock this Obol Agent service. Pay per call in USDC or OBOL.
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
        "Cache-Control": "public, max-age=3600, immutable",
      },
    },
  );
}
