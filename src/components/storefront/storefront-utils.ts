// Branding editor helpers for the operator-only /storefront page.
// Persisted fields must match `obol sell info set` in obol-stack.

export interface BrandingState {
  displayName: string;
  tagline: string;
  theme: 'light' | 'dark' | 'obol';
  accent: string;
  /** Published URL or data:image/... URL from the file picker. */
  logoDataUrl: string | null;
}

export type BrandingField = keyof BrandingState;

export const DEFAULT_BRANDING: BrandingState = {
  displayName: 'Agent Services',
  tagline: 'Unlock Agent and API services with digital payments.',
  theme: 'light',
  accent: '#2FE4AB',
  logoDataUrl: null,
};

export interface ThemeConfig {
  label: string;
  accent: string;
  vars: Record<string, string>;
}

/** Presets match obol-stack `internal/storefront/theme.go`. */
export const THEMES: Record<BrandingState['theme'], ThemeConfig> = {
  light: {
    label: 'Light',
    accent: '#0b9b71',
    vars: {
      bg01: '#ffffff',
      bg02: '#f6f8f9',
      bg03: '#edf1f2',
      bg04: '#e2e8ea',
      bg05: '#d5dedf',
      stroke: '#dfe6e8',
      green: '#0b9b71',
      'green-dim': '#067a57',
      light: '#0e1b1e',
      body: '#40565c',
      muted: '#8299a0',
      red: '#c94f2f',
      amber: '#b96f10',
    },
  },
  dark: {
    label: 'Dark',
    accent: '#2FE4AB',
    vars: {
      bg01: '#091011',
      bg02: '#111f22',
      bg03: '#182d32',
      bg04: '#243d42',
      bg05: '#2d4d53',
      stroke: '#1e3a3f',
      green: '#2fe4ab',
      'green-dim': '#1a7a5c',
      light: '#d9eef3',
      body: '#9cc2c9',
      muted: '#475e64',
      red: '#ff7a7a',
      amber: '#e89e30',
    },
  },
  obol: {
    label: 'Obol',
    accent: '#2FE4AB',
    vars: {
      bg01: '#05201a',
      bg02: '#0a2c23',
      bg03: '#0f3a2e',
      bg04: '#164a3b',
      bg05: '#1d5a48',
      stroke: '#1b4a3b',
      green: '#2fe4ab',
      'green-dim': '#1a7a5c',
      light: '#e4fbf2',
      body: '#a3d8c4',
      muted: '#59836f',
      red: '#ff8a70',
      amber: '#e8b04a',
    },
  },
};

export const STOREFRONT_PREVIEW_MESSAGE = 'obol.storefront.preview';
export const STOREFRONT_PREVIEW_READY_MESSAGE = 'obol.storefront.preview.ready';

/** Session-only payload consumed by the real public storefront iframe. */
export function buildPreviewMessage(state: BrandingState) {
  const themeVars = {
    ...THEMES[state.theme].vars,
    green: state.accent,
    'green-dim': state.accent,
  };
  return {
    type: STOREFRONT_PREVIEW_MESSAGE,
    version: 1,
    branding: {
      displayName: state.displayName,
      tagline: state.tagline,
      theme: state.theme,
      themeVars,
      logoUrl: state.logoDataUrl,
    },
  } as const;
}

export const ACCENT_SWATCHES = [
  '#2FE4AB',
  '#0b9b71',
  '#6366F1',
  '#F97316',
  '#E11D6B',
  '#0EA5E9',
  '#111111',
];

function shellQuote(s: string): string {
  // Prefer single quotes for CLI; escape any embedded singles.
  return `'${s.replace(/'/g, `'\\''`)}'`;
}

function assetFlag(urlFlag: string, value: string): string {
  return `  ${urlFlag} ${shellQuote(value)}`;
}

/**
 * Build a real `obol sell info set` command.
 * Only emits fields changed by the operator. A reset command is included only
 * when the operator explicitly clears a field whose CLI representation cannot
 * be an empty patch value.
 */
export function buildCliCommand(
  state: BrandingState,
  changed: ReadonlySet<BrandingField>
): string {
  const setFlags: string[] = [];
  const resetFlags: string[] = [];

  const setText = (
    field: BrandingField,
    flag: string,
    value: string,
    resetFlag = flag
  ) => {
    if (!changed.has(field)) return;
    if (value.trim()) setFlags.push(`  ${flag} ${shellQuote(value)}`);
    else resetFlags.push(`  ${resetFlag}`);
  };
  const setAsset = (
    field: BrandingField,
    setFlag: string,
    resetFlag: string,
    value: string | null
  ) => {
    if (!changed.has(field)) return;
    if (value) setFlags.push(assetFlag(setFlag, value));
    else resetFlags.push(`  ${resetFlag}`);
  };

  setText('displayName', '--display-name', state.displayName);
  setText('tagline', '--tagline', state.tagline);
  if (changed.has('theme')) setFlags.push(`  --theme ${state.theme}`);
  setText('accent', '--accent', state.accent);
  setAsset('logoDataUrl', '--logo-url', '--logo-url', state.logoDataUrl);

  const commands: string[] = [];
  if (resetFlags.length) {
    commands.push(['obol sell info reset', ...resetFlags].join(' \\\n'));
  }
  if (setFlags.length) {
    commands.push(['obol sell info set', ...setFlags].join(' \\\n'));
  }
  return commands.join(' && \\\n');
}

export function buildClaudeCodePrompt(
  state: BrandingState,
  changed: ReadonlySet<BrandingField>
): string {
  return `Update my Obol storefront branding using the below command:\n${buildCliCommand(state, changed)}`;
}
