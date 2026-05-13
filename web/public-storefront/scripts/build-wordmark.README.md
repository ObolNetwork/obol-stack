# Rebuilding the wordmark PNG

`public/obol-stack-logo.svg` is the canonical wordmark. The sibling
`public/obol-stack-logo.png` is a 3× raster of that SVG, kept around for
consumers that need bytes rather than vector (currently: the x402 verifier's
HTTP 402 HTML template fetches it as `<img src="{StorefrontURL}/obol-stack-logo.png">`,
and the Next OG image generation reads it from `public/` at build time).

Anytime the SVG changes, regenerate the PNG so the two stay in sync:

```bash
# 1. Edit public/obol-stack-logo.svg (and mirror the change in
#    scripts/build-wordmark.html — the HTML wraps the SVG in a page
#    that loads DM Sans from Google Fonts so the "Stack" text renders).

# 2. Render at 3× device pixel ratio with chrome-headless-shell:
CHROME=~/Library/Caches/ms-playwright/chromium_headless_shell-1217/chrome-headless-shell-mac-arm64/chrome-headless-shell
"$CHROME" --headless --disable-gpu --hide-scrollbars \
  --force-device-scale-factor=3 \
  --window-size=165,28 \
  --default-background-color=00000000 \
  --virtual-time-budget=3000 \
  --screenshot=public/obol-stack-logo.png \
  file://"$(pwd)"/scripts/build-wordmark.html

# 3. Mirror to the operator UI:
cp public/obol-stack-logo.png    ../../../obol-stack-front-end/public/
cp public/obol-stack-logo.svg    ../../../obol-stack-front-end/public/
```

The output should be a 495×84 RGBA PNG (1×: 165×28 viewBox). If the rendered
"Stack" text looks wrong, check that Google Fonts is reachable — DM Sans must
load for the text to vector-rasterise correctly.

Any chrome-headless-shell binary that supports `--force-device-scale-factor`
works; the cached Playwright copy is just convenient. On a fresh machine, run
`pnpm exec playwright install chromium` from the operator UI repo first.
