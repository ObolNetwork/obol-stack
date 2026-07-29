'use client';

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { BrandingPanel } from './BrandingPanel';
import {
  buildPreviewMessage,
  DEFAULT_BRANDING,
  STOREFRONT_PREVIEW_READY_MESSAGE,
  type BrandingField,
  type BrandingState,
} from './storefront-utils';
import {
  localStorefrontPreviewUrl,
  publicStorefrontUrl,
} from '@/lib/services/public-storefront';
import { useTunnelURL } from '@/hooks/useAgents';

const BRANDING_FIELDS: BrandingField[] = [
  'displayName',
  'tagline',
  'theme',
  'accent',
  'logoDataUrl',
];

function publishedBranding(data: unknown): BrandingState | null {
  if (!data || typeof data !== 'object') return null;
  const published = (data as { published?: unknown }).published;
  if (!published || typeof published !== 'object') return null;
  const value = published as Record<string, unknown>;
  if (
    typeof value.displayName !== 'string' ||
    typeof value.tagline !== 'string' ||
    (value.theme !== 'light' &&
      value.theme !== 'dark' &&
      value.theme !== 'obol') ||
    typeof value.accent !== 'string'
  ) {
    return null;
  }
  const image = (field: string): string | null =>
    typeof value[field] === 'string' ? value[field] : null;
  return {
    displayName: value.displayName,
    tagline: value.tagline,
    theme: value.theme,
    accent: value.accent,
    logoDataUrl: image('logoUrl'),
  };
}

/**
 * Operator-only branding editor.
 *
 * Does NOT reimplement the buyer storefront. The live preview iframe points at
 * the local-only `storefront-preview.obol.stack` renderer; "Open in new tab"
 * uses the public tunnel URL when available.
 */
export function BrandingEditorPage() {
  const { tunnelURL } = useTunnelURL();
  const publicUrl = useMemo(() => publicStorefrontUrl(tunnelURL), [tunnelURL]);
  const previewUrl = useMemo(
    () =>
      typeof window === 'undefined'
        ? localStorefrontPreviewUrl(null)
        : localStorefrontPreviewUrl(window.location.origin),
    []
  );
  const previewOrigin = useMemo(() => new URL(previewUrl).origin, [previewUrl]);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const initializedRef = useRef(false);
  const [initialized, setInitialized] = useState(false);
  const [baseline, setBaseline] = useState<BrandingState>(DEFAULT_BRANDING);
  const [branding, setBranding] = useState<BrandingState>(DEFAULT_BRANDING);

  const changedFields = useMemo(
    () =>
      new Set(
        BRANDING_FIELDS.filter(field => branding[field] !== baseline[field])
      ),
    [baseline, branding]
  );

  const sendPreview = useCallback(() => {
    if (!initialized) return;
    iframeRef.current?.contentWindow?.postMessage(
      buildPreviewMessage(branding),
      previewOrigin
    );
  }, [branding, initialized, previewOrigin]);

  useEffect(() => {
    sendPreview();
  }, [sendPreview]);

  useEffect(() => {
    const handleReady = (event: MessageEvent) => {
      if (
        event.source !== iframeRef.current?.contentWindow ||
        event.origin !== previewOrigin ||
        event.data?.type !== STOREFRONT_PREVIEW_READY_MESSAGE ||
        event.data?.version !== 1
      ) {
        return;
      }
      if (!initializedRef.current) {
        const published = publishedBranding(event.data);
        if (!published) return;
        initializedRef.current = true;
        setBaseline(published);
        setBranding(published);
        setInitialized(true);
        return;
      }
      sendPreview();
    };
    window.addEventListener('message', handleReady);
    return () => window.removeEventListener('message', handleReady);
  }, [previewOrigin, sendPreview]);

  const handleReset = useCallback(() => {
    setBranding(baseline);
  }, [baseline]);

  const update = useCallback(
    <K extends BrandingField>(field: K, value: BrandingState[K]) => {
      setBranding(current => ({ ...current, [field]: value }));
    },
    []
  );

  return (
    <div className="shop-editor">
      {initialized ? (
        <BrandingPanel
          displayName={branding.displayName}
          tagline={branding.tagline}
          onDisplayNameChange={value => update('displayName', value)}
          onTaglineChange={value => update('tagline', value)}
          logoDataUrl={branding.logoDataUrl}
          onLogoChange={value => update('logoDataUrl', value)}
          accent={branding.accent}
          onAccentChange={value => update('accent', value)}
          theme={branding.theme}
          onThemeChange={value => update('theme', value)}
          onReset={handleReset}
          branding={branding}
          changedFields={changedFields}
        />
      ) : (
        <aside className="shop-panel shop-panel-embedded" aria-busy="true">
          <div style={{ padding: 26, color: '#8B9794' }}>
            Loading published storefront…
          </div>
        </aside>
      )}

      <section className="shop-preview" aria-label="Draft storefront preview">
        <div className="shop-preview-toolbar">
          <div>
            <div className="shop-preview-label">Draft storefront preview</div>
            <div className="shop-preview-url" title={previewUrl}>
              {previewUrl}
              <span className="shop-preview-hint">
                {' '}
                (local-only preview — not the public tunnel)
              </span>
            </div>
          </div>
          <div className="shop-preview-actions">
            <a
              className="shop-btn shop-btn-primary"
              href={publicUrl}
              target="_blank"
              rel="noopener noreferrer"
            >
              Open published
            </a>
          </div>
        </div>
        <iframe
          ref={iframeRef}
          className="shop-preview-frame"
          title="Draft public storefront preview"
          src={previewUrl}
          onLoad={sendPreview}
          sandbox="allow-scripts allow-same-origin"
          referrerPolicy="strict-origin"
        />
      </section>
    </div>
  );
}
