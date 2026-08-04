'use client';

import { useState, useCallback, useRef } from 'react';
import { ColorPicker } from './ColorPicker';
import { CommandModal } from './CommandModal';
import type { BrandingField, BrandingState } from './storefront-utils';
import { THEMES, ACCENT_SWATCHES, isHttpLogoUrl } from './storefront-utils';

const MAX_INLINE_IMAGE_BYTES = 256 * 1024;

interface Props {
  displayName: string;
  tagline: string;
  onDisplayNameChange: (v: string) => void;
  onTaglineChange: (v: string) => void;
  logoDataUrl: string | null;
  onLogoChange: (v: string | null) => void;
  accent: string;
  onAccentChange: (v: string) => void;
  theme: BrandingState['theme'];
  onThemeChange: (v: BrandingState['theme']) => void;
  onReset: () => void;
  branding: BrandingState;
  changedFields: ReadonlySet<BrandingField>;
}

function Swatch({
  color,
  active,
  onClick,
}: {
  color: string;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      title={color}
      type="button"
      style={{
        width: 30,
        height: 30,
        borderRadius: 8,
        cursor: 'pointer',
        background: color,
        border: active
          ? '2px solid var(--ink)'
          : '1px solid var(--line-strong)',
        boxShadow: active ? '0 0 0 2px var(--surface) inset' : 'none',
        padding: 0,
      }}
    />
  );
}

const LABEL_STYLE: React.CSSProperties = {
  fontFamily: 'var(--font-dm-mono), ui-monospace, monospace',
  fontSize: 11,
  textTransform: 'uppercase',
  letterSpacing: '0.08em',
  color: '#8B9794',
};

function FileRow({
  label,
  value,
  onChange,
  onClear,
  accept = 'image/*',
  hint,
}: {
  label: string;
  value: string | null;
  onChange: (dataUrl: string) => void;
  onClear: () => void;
  accept?: string;
  hint?: string;
}) {
  const ref = useRef<HTMLInputElement>(null);
  const [mode, setMode] = useState<'upload' | 'url'>(() =>
    isHttpLogoUrl(value) ? 'url' : 'upload'
  );
  const [urlDraft, setUrlDraft] = useState(() =>
    isHttpLogoUrl(value) ? value! : ''
  );
  const [error, setError] = useState<string | null>(null);

  const applyUrl = useCallback(() => {
    const trimmed = urlDraft.trim();
    if (!isHttpLogoUrl(trimmed)) {
      setError('Paste a valid http:// or https:// image URL.');
      return;
    }
    setError(null);
    onChange(trimmed);
  }, [onChange, urlDraft]);

  const onFile = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (!file) return;
      e.target.value = '';
      if (!file.type.startsWith('image/')) {
        setError('Choose a PNG, JPEG, GIF, WebP, SVG, or ICO image.');
        return;
      }
      if (file.size > MAX_INLINE_IMAGE_BYTES) {
        setError(
          'Upload must be 256 KiB or smaller. Prefer Image URL for larger logos.'
        );
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        setError(null);
        onChange(reader.result as string);
      };
      reader.onerror = () => setError('Could not read this image.');
      reader.readAsDataURL(file);
    },
    [onChange]
  );

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <span style={LABEL_STYLE}>{label}</span>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button
          type="button"
          className="shop-chip"
          data-active={mode === 'upload'}
          onClick={() => setMode('upload')}
        >
          Upload
        </button>
        <button
          type="button"
          className="shop-chip"
          data-active={mode === 'url'}
          onClick={() => setMode('url')}
        >
          Image URL
        </button>
      </div>

      {mode === 'upload' ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <div
            style={{
              width: 40,
              height: 40,
              borderRadius: 8,
              flexShrink: 0,
              overflow: 'hidden',
              border: '1px solid #DFDED6',
              background: '#F4F4F1',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
            }}
          >
            {value ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={value}
                alt=""
                style={{ width: '100%', height: '100%', objectFit: 'contain' }}
              />
            ) : (
              <span style={{ fontSize: 11, color: '#8B9794' }}>—</span>
            )}
          </div>
          <label
            className="shop-btn"
            style={{ cursor: 'pointer', borderColor: '#DFDED6' }}
          >
            {value && value.startsWith('data:') ? 'Replace' : 'Upload'}
            <input
              ref={ref}
              type="file"
              accept={accept}
              onChange={onFile}
              style={{ display: 'none' }}
            />
          </label>
          {value && (
            <button
              className="shop-modal-close"
              onClick={() => {
                setUrlDraft('');
                onClear();
              }}
              type="button"
              aria-label={`Remove ${label}`}
            >
              ×
            </button>
          )}
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          <input
            className="shop-input"
            type="url"
            placeholder="https://cdn.example.com/logo.png"
            value={urlDraft}
            onChange={e => setUrlDraft(e.target.value)}
            onKeyDown={e => {
              if (e.key === 'Enter') {
                e.preventDefault();
                applyUrl();
              }
            }}
          />
          <div style={{ display: 'flex', gap: 8 }}>
            <button
              className="shop-btn"
              type="button"
              onClick={applyUrl}
              style={{ borderColor: '#DFDED6' }}
            >
              Use URL
            </button>
            {value && (
              <button
                className="shop-modal-close"
                onClick={() => {
                  setUrlDraft('');
                  onClear();
                }}
                type="button"
                aria-label={`Remove ${label}`}
              >
                ×
              </button>
            )}
          </div>
          {isHttpLogoUrl(value) && (
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <div
                style={{
                  width: 40,
                  height: 40,
                  borderRadius: 8,
                  overflow: 'hidden',
                  border: '1px solid #DFDED6',
                  background: '#F4F4F1',
                }}
              >
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={value!}
                  alt=""
                  style={{
                    width: '100%',
                    height: '100%',
                    objectFit: 'contain',
                  }}
                />
              </div>
              <span
                style={{
                  fontSize: 11.5,
                  color: '#8B9794',
                  overflowWrap: 'anywhere',
                }}
              >
                {value}
              </span>
            </div>
          )}
        </div>
      )}

      {hint && <span style={{ fontSize: 11.5, color: '#8B9794' }}>{hint}</span>}
      {error && (
        <span role="alert" style={{ fontSize: 11.5, color: '#C94F2F' }}>
          {error}
        </span>
      )}
    </div>
  );
}

export function BrandingPanel({
  displayName,
  tagline,
  onDisplayNameChange,
  onTaglineChange,
  logoDataUrl,
  onLogoChange,
  accent,
  onAccentChange,
  theme,
  onThemeChange,
  onReset,
  branding,
  changedFields,
}: Props) {
  const [showCommand, setShowCommand] = useState(false);
  const hasChanges = changedFields.size > 0;

  return (
    <>
      <aside
        className="shop-panel shop-panel-embedded"
        aria-label="Storefront branding"
      >
        <div style={{ padding: '26px 26px 0' }}>
          <div
            style={{
              fontFamily: 'var(--font-dm-mono), ui-monospace, monospace',
              fontSize: 10.5,
              textTransform: 'uppercase',
              letterSpacing: '0.1em',
              color: '#8B9794',
              marginBottom: 6,
            }}
          >
            Storefront branding
          </div>
          <h1
            style={{
              margin: 0,
              fontSize: 22,
              fontWeight: 600,
              color: 'var(--ink)',
            }}
          >
            Make it yours
          </h1>
          <p style={{ margin: '8px 0 0', fontSize: 13, color: '#8B9794' }}>
            Operator-only editor. Preview uses the real public storefront —
            apply with <code>obol sell info set</code>.
          </p>
        </div>

        <div
          style={{
            padding: 26,
            display: 'flex',
            flexDirection: 'column',
            gap: 26,
          }}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <span style={LABEL_STYLE}>Theme</span>
            <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
              {(Object.keys(THEMES) as BrandingState['theme'][]).map(key => (
                <button
                  key={key}
                  type="button"
                  className="shop-chip"
                  data-active={theme === key}
                  onClick={() => onThemeChange(key)}
                >
                  {THEMES[key].label}
                </button>
              ))}
            </div>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <span style={LABEL_STYLE}>Accent</span>
            <div
              style={{
                display: 'flex',
                gap: 8,
                flexWrap: 'wrap',
                alignItems: 'center',
              }}
            >
              {ACCENT_SWATCHES.map(c => (
                <Swatch
                  key={c}
                  color={c}
                  active={accent.toLowerCase() === c.toLowerCase()}
                  onClick={() => onAccentChange(c)}
                />
              ))}
              <ColorPicker
                label="Custom accent"
                value={accent}
                onChange={onAccentChange}
              />
            </div>
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span style={LABEL_STYLE}>Display name</span>
            <input
              className="shop-input"
              value={displayName}
              onChange={e => onDisplayNameChange(e.target.value)}
              type="text"
            />
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
            <span style={LABEL_STYLE}>Tagline</span>
            <input
              className="shop-input"
              value={tagline}
              onChange={e => onTaglineChange(e.target.value)}
              type="text"
            />
          </div>

          <FileRow
            label="Logo"
            value={logoDataUrl}
            onChange={onLogoChange}
            onClear={() => onLogoChange(null)}
            hint="Prefer Image URL for large logos. Upload embeds a short data URI (max 256 KiB) — browsers cannot expose real disk paths to the CLI."
          />

          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <button
              className="shop-btn shop-btn-primary"
              onClick={() => setShowCommand(true)}
              type="button"
              disabled={!hasChanges}
              style={{ width: '100%' }}
            >
              {hasChanges ? 'Copy apply command' : 'No changes to apply'}
            </button>
            <button
              className="shop-btn"
              onClick={onReset}
              type="button"
              disabled={!hasChanges}
              style={{ width: '100%', borderColor: '#DFDED6' }}
            >
              Discard changes
            </button>
          </div>

          <p
            style={{
              margin: 0,
              fontSize: 12.5,
              lineHeight: 1.5,
              color: '#8B9794',
            }}
          >
            Draft changes appear in the preview instantly but are visible only
            to you. Copy and run the command to publish the selected values.
          </p>
        </div>
      </aside>

      {showCommand && (
        <CommandModal
          branding={branding}
          changedFields={changedFields}
          onClose={() => setShowCommand(false)}
        />
      )}
    </>
  );
}
