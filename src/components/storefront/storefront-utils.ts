import type { ServiceListItem } from '@/lib/marketplace/types';
import type { ServiceOfferType } from '@/types/sell';
import {
  PRICE_UNIT_LABEL,
  chainLabel,
  statusTone,
  statusLabel,
} from '@/components/marketplace/service-display';

export type StorefrontCategory = 'all' | 'agent' | 'api' | 'demo';

export interface StorefrontCardProps {
  id: string;
  name: string;
  initials: string;
  provider: string;
  blurb: string;
  tags: string[];
  cat: StorefrontCategory;
  price: string;
  priceSymbol: string;
  priceUnit: string;
  chain: string;
  featured: boolean;
  statusTone: 'live' | 'pending' | 'degraded';
  statusLabel: string;
}

function getInitials(name: string): string {
  const words = name.trim().split(/\s+/);
  if (words.length >= 2) {
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  return name.slice(0, 2).toUpperCase();
}

function getCategory(
  type: ServiceOfferType,
  isDemo: boolean
): StorefrontCategory {
  if (isDemo) return 'demo';
  switch (type) {
    case 'inference':
    case 'agent':
    case 'fine-tuning':
      return 'agent';
    case 'http':
      return 'api';
    default:
      return 'agent';
  }
}

export function toCardProps(item: ServiceListItem): StorefrontCardProps {
  return {
    id: item.slug,
    name: item.name,
    initials: getInitials(item.name),
    provider: item.namespace,
    blurb: item.description,
    tags: item.skills,
    cat: getCategory(item.type, item.isDemo),
    price: item.price || '—',
    priceSymbol: item.asset.symbol,
    priceUnit: PRICE_UNIT_LABEL[item.priceUnit] ?? '',
    chain: chainLabel(item),
    featured: item.status === 'live',
    statusTone: statusTone(item.status),
    statusLabel: statusLabel(item.status),
  };
}

export function countByCategory(
  items: StorefrontCardProps[]
): Record<StorefrontCategory, number> {
  const counts: Record<StorefrontCategory, number> = {
    all: items.length,
    agent: 0,
    api: 0,
    demo: 0,
  };
  for (const item of items) {
    counts[item.cat]++;
  }
  return counts;
}

export function filterByCategory(
  items: StorefrontCardProps[],
  cat: StorefrontCategory
): StorefrontCardProps[] {
  if (cat === 'all') return items;
  return items.filter(i => i.cat === cat);
}

export function filterBySearch(
  items: StorefrontCardProps[],
  query: string
): StorefrontCardProps[] {
  const q = query.toLowerCase().trim();
  if (!q) return items;
  return items.filter(
    i =>
      i.name.toLowerCase().includes(q) ||
      i.provider.toLowerCase().includes(q) ||
      i.blurb.toLowerCase().includes(q) ||
      i.tags.some(t => t.toLowerCase().includes(q))
  );
}

// ── Colour helpers (from prototype) ─────────────────────────────────────

function hexToRgb(h: string): [number, number, number] {
  const m = h.replace('#', '');
  const n =
    m.length === 3
      ? m
          .split('')
          .map(c => c + c)
          .join('')
      : m;
  return [
    parseInt(n.slice(0, 2), 16),
    parseInt(n.slice(2, 4), 16),
    parseInt(n.slice(4, 6), 16),
  ];
}

function mix(hex: string, target: [number, number, number], t: number): string {
  const [r, g, b] = hexToRgb(hex);
  const c = (a: number, d: number) => Math.round(a + (d - a) * t);
  return `rgb(${c(r, target[0])}, ${c(g, target[1])}, ${c(b, target[2])})`;
}

function rgba(hex: string, a: number): string {
  const [r, g, b] = hexToRgb(hex);
  return `rgba(${r}, ${g}, ${b}, ${a})`;
}

function readableOn(hex: string): string {
  const [r, g, b] = hexToRgb(hex);
  const lum = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
  return lum > 0.6 ? '#08201A' : '#FFFFFF';
}

/** Derive --ink-2 and --ink-3 from a primary ink colour toward the current bg. */
export function applyInk(ink: string): void {
  const root = document.documentElement.style;
  root.setProperty('--ink', ink);
  const bgVal =
    getComputedStyle(document.documentElement)
      .getPropertyValue('--bg')
      .trim() || '#F4F4F1';
  const [br, bgc, bb] = hexToRgb(bgVal.startsWith('#') ? bgVal : '#F4F4F1');
  const toward = (t: number) => {
    const [r, g, b] = hexToRgb(ink);
    const c = (a: number, d: number) => Math.round(a + (d - a) * t);
    return `rgb(${c(r, br)}, ${c(g, bgc)}, ${c(b, bb)})`;
  };
  root.setProperty('--ink-2', toward(0.34));
  root.setProperty('--ink-3', toward(0.56));
}

/**
 * Apply accent and/or bg colour. When bg is set, the function derives
 * surface, card, ink, ink-2, ink-3, line, and line-strong from it —
 * flipping light/dark automatically based on luminance.
 */
export function applyBrand(accent: string | null, bg: string | null): void {
  const root = document.documentElement.style;
  if (accent) {
    root.setProperty('--brand', mix(accent, [0, 0, 0], 0.12));
    root.setProperty('--brand-btn', accent);
    root.setProperty('--brand-btn-h', mix(accent, [255, 255, 255], 0.28));
    root.setProperty('--brand-soft', rgba(accent, 0.09));
    root.setProperty('--brand-soft-h', rgba(accent, 0.15));
    root.setProperty('--accent-fg', readableOn(accent));
  }
  if (bg) {
    root.setProperty('--bg', bg);
    const [r, g, b] = hexToRgb(bg);
    const lum = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
    if (lum < 0.5) {
      const surf = mix(bg, [255, 255, 255], 0.06);
      root.setProperty('--surface', surf);
      root.setProperty('--card', surf);
      root.setProperty('--ink', mix(bg, [255, 255, 255], 0.92));
      root.setProperty('--ink-2', mix(bg, [255, 255, 255], 0.62));
      root.setProperty('--ink-3', mix(bg, [255, 255, 255], 0.42));
      root.setProperty('--line', mix(bg, [255, 255, 255], 0.12));
      root.setProperty('--line-strong', mix(bg, [255, 255, 255], 0.2));
    } else {
      const surf = mix(bg, [255, 255, 255], 0.55);
      root.setProperty('--surface', surf);
      root.setProperty('--card', surf);
      root.setProperty('--ink', mix(bg, [0, 0, 0], 0.9));
      root.setProperty('--ink-2', mix(bg, [0, 0, 0], 0.62));
      root.setProperty('--ink-3', mix(bg, [0, 0, 0], 0.42));
      root.setProperty('--line', mix(bg, [0, 0, 0], 0.08));
      root.setProperty('--line-strong', mix(bg, [0, 0, 0], 0.14));
    }
  }
}

export function applyCardColor(card: string): void {
  document.documentElement.style.setProperty('--card', card);
}

// ── Branding state ──────────────────────────────────────────────────────

export interface BrandingState {
  displayName: string;
  tagline: string;
  theme: 'light' | 'dark' | 'obol';
  accent: string;
  bgColor: string;
  textColor: string;
  cardColor: string;
  logoFile: string | null;
  faviconFile: string | null;
  ogFile: string | null;
}

export const DEFAULT_BRANDING: BrandingState = {
  displayName: 'Agent Services',
  tagline: 'Unlock Agent and API services with digital payments.',
  theme: 'light',
  accent: '#2FE4AB',
  bgColor: '#F4F4F1',
  textColor: '#16211F',
  cardColor: '#FFFFFF',
  logoFile: null,
  faviconFile: null,
  ogFile: null,
};

// Theme presets — matching the prototype exactly. Dark uses blue accent,
// Obol uses green accent. This is the key differentiator.
export interface ThemeConfig {
  label: string;
  accent: string;
  bg: string;
  text: string;
}

export const THEMES: Record<BrandingState['theme'], ThemeConfig> = {
  light: { label: 'Light', accent: '#2FE4AB', bg: '#F4F4F1', text: '#16211F' },
  dark: { label: 'Dark', accent: '#7C9CFF', bg: '#0C0C0D', text: '#EDEDED' },
  obol: { label: 'Obol', accent: '#2FE4AB', bg: '#091011', text: '#DFEAED' },
};

export const ACCENT_SWATCHES = [
  '#2FE4AB',
  '#6366F1',
  '#F97316',
  '#E11D6B',
  '#0EA5E9',
  '#111111',
];
export const BG_SWATCHES = [
  '#F4F4F1',
  '#FFFFFF',
  '#F1F5F9',
  '#FAF5EE',
  '#F3F0FA',
  '#0E1512',
];
export const TEXT_SWATCHES = [
  '#16211F',
  '#1E293B',
  '#3A2E22',
  '#241B33',
  '#E1E9EB',
  '#FFFFFF',
];
export const CARD_SWATCHES = [
  '#FFFFFF',
  '#FBFAF5',
  '#F1F5F9',
  '#1A2A2E',
  '#16181B',
  '#182D32',
];

export function clearThemeOverrides(): void {
  const vars = [
    '--bg',
    '--surface',
    '--card',
    '--ink',
    '--ink-2',
    '--ink-3',
    '--line',
    '--line-strong',
    '--brand',
    '--brand-btn',
    '--brand-btn-h',
    '--brand-soft',
    '--brand-soft-h',
    '--accent-fg',
  ];
  const el = document.documentElement;
  for (const v of vars) {
    el.style.removeProperty(v);
  }
}

export function buildCliCommand(state: BrandingState): string {
  const q = (s: string) => `"${s.replace(/"/g, '\\"')}"`;
  const flags = [
    `  --display-name ${q(state.displayName)}`,
    `  --tagline ${q(state.tagline)}`,
    state.logoFile ? '  --logo-file ./logo.png' : null,
    state.faviconFile ? '  --favicon-file ./favicon.png' : null,
    state.ogFile ? '  --og-image-file ./og-image.png' : null,
    `  --theme ${state.theme} --accent '${state.accent}'`,
    `  --bg '${state.bgColor}'`,
    `  --text-color '${state.textColor}'`,
    `  --card-color '${state.cardColor}'`,
  ].filter(Boolean);
  return ['obol sell info set', ...flags].join(' \\\n');
}

export function buildClaudeCodePrompt(state: BrandingState): string {
  const cmd = buildCliCommand(state);
  return `Update my Obol storefront branding using the below command:\n${cmd}`;
}
