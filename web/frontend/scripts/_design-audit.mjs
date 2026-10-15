// Headless-render the three live Obol Stack UIs.
//
// Why --host-resolver-rules: the tunnel is currently dormant so the public
// trycloudflare hostname doesn't resolve. We remap it to localhost:8080 where
// Traefik routes by Host header, which is what Cloudflared would normally do.

import { chromium } from "playwright";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "obol-design-audit-"));
fs.mkdirSync(outDir, { recursive: true });

const TUNNEL_HOST = "cigarette-checking-journalist-processed.trycloudflare.com";

const targets = [
  {
    name: "operator-ui",
    url: "http://obol.stack:8080/",
    viewport: { width: 1440, height: 900 },
  },
  {
    name: "storefront",
    // Browser resolves this to 127.0.0.1:8080 via --host-resolver-rules below.
    // Traefik then routes on Host header to the tunnel-storefront route.
    url: `http://${TUNNEL_HOST}/`,
    viewport: { width: 1440, height: 900 },
  },
  {
    name: "operator-ui-mobile",
    url: "http://obol.stack:8080/",
    viewport: { width: 390, height: 844 },
  },
  {
    name: "storefront-mobile",
    url: `http://${TUNNEL_HOST}/`,
    viewport: { width: 390, height: 844 },
  },
];

const browser = await chromium.launch({
  args: [
    `--host-resolver-rules=MAP ${TUNNEL_HOST} 127.0.0.1:8080`,
    "--ignore-certificate-errors",
  ],
});

try {
  for (const t of targets) {
    const ctx = await browser.newContext({ viewport: t.viewport });
    const page = await ctx.newPage();
    console.log(`render: ${t.name} <- ${t.url}`);
    try {
      const resp = await page.goto(t.url, {
        waitUntil: "networkidle",
        timeout: 30000,
      });
      console.log(`  status: ${resp?.status()}`);
      // Settle hydration/animations.
      await page.waitForTimeout(1500);
      await page.screenshot({
        path: `${outDir}/${t.name}.png`,
        fullPage: true,
      });
      const html = await page.content();
      fs.writeFileSync(`${outDir}/${t.name}.html`, html);
    } catch (err) {
      console.log(`  ERROR: ${err.message}`);
      try {
        await page.screenshot({ path: `${outDir}/${t.name}-error.png` });
      } catch {}
    } finally {
      await ctx.close();
    }
  }
} finally {
  await browser.close();
}
console.log("done");
