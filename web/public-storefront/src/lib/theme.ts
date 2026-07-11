// Theme plumbing for the operator-selectable presets (light/dark/obol).
//
// The controller publishes the RESOLVED token→hex map in the catalog
// envelope (`themeVars`, accent override already applied), so this module
// never needs the preset tables — it only maps bare token names onto the
// Tailwind CSS variables declared in globals.css and provides the light
// fallback for catalogs published by pre-theming controllers.
//
// Fallback values mirror internal/storefront/theme.go (ThemeLight). Keep in
// sync if the Go preset changes.

export const LIGHT_THEME_VARS: Record<string, string> = {
  bg01: "#ffffff",
  bg02: "#f6f8f9",
  bg03: "#edf1f2",
  bg04: "#e2e8ea",
  bg05: "#d5dedf",
  stroke: "#dfe6e8",
  green: "#0b9b71",
  "green-dim": "#067a57",
  light: "#0e1b1e",
  body: "#40565c",
  muted: "#8299a0",
  red: "#c94f2f",
  amber: "#b96f10",
};

// Bare catalog token → CSS custom property consumed by the Tailwind theme.
const TOKEN_TO_CSS_VAR: Record<string, string> = {
  bg01: "--color-bg01",
  bg02: "--color-bg02",
  bg03: "--color-bg03",
  bg04: "--color-bg04",
  bg05: "--color-bg05",
  stroke: "--color-stroke",
  green: "--color-obol-green",
  "green-dim": "--color-obol-green-dim",
  light: "--color-text-light",
  body: "--color-text-body",
  muted: "--color-text-muted",
  red: "--color-red",
  amber: "--color-amber",
};

const HEX_RE = /^#[0-9a-fA-F]{3,8}$/;

/** Resolved token value with light fallback; only plain hex passes. */
export function themeToken(
  token: string,
  vars?: Record<string, string>,
): string {
  const v = vars?.[token];
  if (v && HEX_RE.test(v)) return v;
  return LIGHT_THEME_VARS[token] ?? "#000000";
}

/**
 * Inline-style object for <html> that overrides the Tailwind theme vars
 * with the published palette. Values are hex-validated (defense in depth —
 * the catalog is operator-authored but tunnel-served).
 */
export function themeStyle(
  vars?: Record<string, string>,
): Record<string, string> {
  const style: Record<string, string> = {};
  for (const [token, cssVar] of Object.entries(TOKEN_TO_CSS_VAR)) {
    style[cssVar] = themeToken(token, vars);
  }
  return style;
}

/** Browser color-scheme hint per preset name; default (light) when unknown. */
export function isDarkTheme(name?: string): boolean {
  return name === "dark" || name === "obol";
}
