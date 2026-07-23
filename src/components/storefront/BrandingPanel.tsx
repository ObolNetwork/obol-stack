'use client';

import { useState, useCallback, useRef, useEffect } from 'react';
import { ColorPicker } from './ColorPicker';
import { CommandModal } from './CommandModal';
import type { BrandingState } from './storefront-utils';
import {
  THEMES,
  ACCENT_SWATCHES,
  BG_SWATCHES,
  TEXT_SWATCHES,
  CARD_SWATCHES,
  applyBrand,
  applyInk,
  applyCardColor,
  clearThemeOverrides,
} from './storefront-utils';

interface Props {
  onClose: () => void;
  onReset: () => void;
  displayName: string;
  tagline: string;
  onDisplayNameChange: (v: string) => void;
  onTaglineChange: (v: string) => void;
  logo: string | null;
  onLogoChange: (v: string | null) => void;
  accent: string;
  onAccentChange: (v: string) => void;
  bg: string;
  onBgChange: (v: string) => void;
  text: string;
  onTextChange: (v: string) => void;
  card: string;
  onCardChange: (v: string) => void;
  theme: BrandingState['theme'];
  onThemeChange: (v: BrandingState['theme']) => void;
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

export function BrandingPanel({
  onClose,
  onReset,
  displayName,
  tagline,
  onDisplayNameChange,
  onTaglineChange,
  logo,
  onLogoChange,
  accent,
  onAccentChange,
  bg,
  onBgChange,
  text,
  onTextChange,
  card,
  onCardChange,
  theme,
  onThemeChange,
}: Props) {
  const [showCommand, setShowCommand] = useState(false);
  const [favicon, setFavicon] = useState<string | null>(null);
  const [ogImage, setOgImage] = useState<string | null>(null);
  const logoRef = useRef<HTMLInputElement>(null);
  const faviconRef = useRef<HTMLInputElement>(null);
  const ogRef = useRef<HTMLInputElement>(null);

  // On unmount, revert overrides
  useEffect(() => {
    return () => {
      clearThemeOverrides();
    };
  }, []);

  const readImg = useCallback((setter: (v: string | null) => void) => {
    return (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (!file) return;
      const reader = new FileReader();
      reader.onload = () => setter(reader.result as string);
      reader.readAsDataURL(file);
    };
  }, []);

  const brandingState: BrandingState = {
    displayName,
    tagline,
    theme,
    accent,
    bgColor: bg,
    textColor: text,
    cardColor: card,
    logoFile: logo ? 'logo.png' : null,
    faviconFile: favicon ? 'favicon.png' : null,
    ogFile: ogImage ? 'og-image.png' : null,
  };

  return (
    <>
      <div className="shop-panel-overlay" onClick={onClose} />
      <div
        className="shop-panel"
        role="dialog"
        aria-modal="true"
        aria-label="Branding customiser"
        onClick={e => e.stopPropagation()}
      >
        {/* Header — matches prototype: "STOREFRONT BRANDING" + Preview tag + "Make it yours" */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            padding: '26px 26px 0',
          }}
        >
          <div style={{ flex: 1 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <div
                style={{
                  fontFamily: 'var(--font-dm-mono), ui-monospace, monospace',
                  fontSize: 10.5,
                  textTransform: 'uppercase',
                  letterSpacing: '0.1em',
                  color: '#8B9794',
                }}
              >
                Storefront branding
              </div>
              <span
                style={{
                  fontFamily: 'var(--font-dm-mono), ui-monospace, monospace',
                  fontSize: 9.5,
                  textTransform: 'uppercase',
                  letterSpacing: '0.1em',
                  color: '#08201A',
                  background: '#2FE4AB',
                  borderRadius: 999,
                  padding: '2px 8px',
                  lineHeight: 1.5,
                }}
              >
                Preview
              </span>
            </div>
            <h2
              style={{
                margin: '5px 0 0',
                fontSize: 18,
                fontWeight: 700,
                color: '#16211F',
              }}
            >
              Make it yours
            </h2>
          </div>
          <button
            className="shop-modal-close"
            onClick={onClose}
            type="button"
            aria-label="Close panel"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>

        <div style={{ flex: 1, padding: '20px 26px', overflowY: 'auto' }}>
          {/* Theme */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Theme</span>
            <div
              className="shop-preset-row"
              style={{
                gap: 4,
                padding: 4,
                background: '#F4F4F1',
                border: '1px solid #DFDED6',
                borderRadius: 10,
              }}
            >
              {(Object.keys(THEMES) as BrandingState['theme'][]).map(k => (
                <button
                  key={k}
                  className="shop-preset"
                  data-active={theme === k}
                  onClick={() => onThemeChange(k)}
                  type="button"
                  style={{
                    background: theme === k ? '#FFFFFF' : 'transparent',
                    boxShadow:
                      theme === k ? '0 1px 2px rgba(0,0,0,0.12)' : 'none',
                    border: 0,
                    borderRadius: 7,
                    color: theme === k ? '#16211F' : '#566461',
                  }}
                >
                  {THEMES[k].label}
                </button>
              ))}
            </div>
          </div>

          {/* Accent colour */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Accent colour</span>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
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
              <ColorPicker label="" value={accent} onChange={onAccentChange} />
            </div>
          </div>

          {/* Page background */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Page background</span>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
                alignItems: 'center',
              }}
            >
              {BG_SWATCHES.map(c => (
                <Swatch
                  key={c}
                  color={c}
                  active={bg.toLowerCase() === c.toLowerCase()}
                  onClick={() => onBgChange(c)}
                />
              ))}
              <ColorPicker label="" value={bg} onChange={onBgChange} />
            </div>
          </div>

          {/* Text colour */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Text colour</span>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
                alignItems: 'center',
              }}
            >
              {TEXT_SWATCHES.map(c => (
                <Swatch
                  key={c}
                  color={c}
                  active={text.toLowerCase() === c.toLowerCase()}
                  onClick={() => onTextChange(c)}
                />
              ))}
              <ColorPicker label="" value={text} onChange={onTextChange} />
            </div>
          </div>

          {/* Card colour */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Card colour</span>
            <div
              style={{
                display: 'flex',
                flexWrap: 'wrap',
                gap: 10,
                alignItems: 'center',
              }}
            >
              {CARD_SWATCHES.map(c => (
                <Swatch
                  key={c}
                  color={c}
                  active={card.toLowerCase() === c.toLowerCase()}
                  onClick={() => onCardChange(c)}
                />
              ))}
              <ColorPicker label="" value={card} onChange={onCardChange} />
            </div>
          </div>

          {/* Logo */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Logo</span>
            {logo ? (
              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    height: 44,
                    minWidth: 80,
                    padding: '0 12px',
                    background: '#F4F4F1',
                    border: '1px solid #DFDED6',
                    borderRadius: 8,
                  }}
                >
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={logo}
                    alt="Logo preview"
                    style={{ height: 26, maxWidth: 150, objectFit: 'contain' }}
                  />
                </div>
                <label
                  className="shop-btn"
                  style={{ cursor: 'pointer', borderColor: '#DFDED6' }}
                >
                  Replace
                  <input
                    type="file"
                    accept="image/*"
                    onChange={readImg(onLogoChange)}
                    style={{ display: 'none' }}
                  />
                </label>
                <button
                  className="shop-modal-close"
                  onClick={() => onLogoChange(null)}
                  type="button"
                  aria-label="Remove logo"
                >
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              </div>
            ) : (
              <label
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  gap: 9,
                  cursor: 'pointer',
                  padding: 18,
                  border: '1px dashed #DFDED6',
                  borderRadius: 10,
                  background: '#F4F4F1',
                  color: '#566461',
                  fontSize: 14,
                  fontWeight: 500,
                  transition: 'border-color 140ms, color 140ms',
                }}
              >
                <svg width="18" height="18" viewBox="0 0 20 20" fill="none">
                  <path
                    d="M10 13V4m0 0L6.5 7.5M10 4l3.5 3.5"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  />
                  <path
                    d="M4 13v2.5h12V13"
                    stroke="currentColor"
                    strokeWidth="1.5"
                    strokeLinecap="round"
                  />
                </svg>
                <span>Upload your logo</span>
                <input
                  ref={logoRef}
                  type="file"
                  accept="image/*"
                  onChange={readImg(onLogoChange)}
                  style={{ display: 'none' }}
                />
              </label>
            )}
            <span style={{ fontSize: 11.5, color: '#8B9794' }}>
              PNG or SVG with transparency works best. Replaces the wordmark in
              the header.
            </span>
          </div>

          {/* Store name + tagline */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 12,
              marginBottom: 26,
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <span style={LABEL_STYLE}>Store name</span>
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
          </div>

          {/* Favicon */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Favicon</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
              <div
                style={{
                  width: 32,
                  height: 32,
                  borderRadius: 7,
                  flexShrink: 0,
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  background: favicon ? '#F4F4F1' : 'var(--brand-btn)',
                  border: '1px solid #DFDED6',
                  overflow: 'hidden',
                }}
              >
                {favicon ? (
                  // eslint-disable-next-line @next/next/no-img-element
                  <img
                    src={favicon}
                    alt=""
                    style={{
                      width: '100%',
                      height: '100%',
                      objectFit: 'contain',
                    }}
                  />
                ) : (
                  <span
                    style={{
                      fontWeight: 700,
                      fontSize: 15,
                      color: 'var(--accent-fg)',
                    }}
                  >
                    {(displayName[0] || 'A').toUpperCase()}
                  </span>
                )}
              </div>
              <label
                className="shop-btn"
                style={{ cursor: 'pointer', borderColor: '#DFDED6' }}
              >
                {favicon ? 'Replace' : 'Upload favicon'}
                <input
                  ref={faviconRef}
                  type="file"
                  accept="image/*"
                  onChange={readImg(setFavicon)}
                  style={{ display: 'none' }}
                />
              </label>
              {favicon && (
                <button
                  className="shop-modal-close"
                  onClick={() => setFavicon(null)}
                  type="button"
                  aria-label="Remove favicon"
                >
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              )}
            </div>
          </div>

          {/* OG / social preview */}
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 10,
              marginBottom: 26,
            }}
          >
            <span style={LABEL_STYLE}>Social preview (OG image)</span>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              {ogImage && (
                <div
                  style={{
                    width: 56,
                    height: 30,
                    borderRadius: 5,
                    flexShrink: 0,
                    overflow: 'hidden',
                    border: '1px solid #DFDED6',
                  }}
                >
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={ogImage}
                    alt="OG preview"
                    style={{
                      width: '100%',
                      height: '100%',
                      objectFit: 'cover',
                      display: 'block',
                    }}
                  />
                </div>
              )}
              <label
                className="shop-btn"
                style={{ cursor: 'pointer', borderColor: '#DFDED6' }}
              >
                {ogImage ? 'Replace' : 'Upload OG image'}
                <input
                  ref={ogRef}
                  type="file"
                  accept="image/*"
                  onChange={readImg(setOgImage)}
                  style={{ display: 'none' }}
                />
              </label>
              {ogImage && (
                <button
                  className="shop-modal-close"
                  onClick={() => setOgImage(null)}
                  type="button"
                  aria-label="Remove OG image"
                >
                  <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              )}
            </div>
            <span style={{ fontSize: 11.5, color: '#8B9794' }}>
              No upload? A card is generated from your name, tagline, and accent
              colour so links unfurl properly.
            </span>
          </div>

          {/* Actions */}
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            <button
              className="shop-btn shop-btn-primary"
              onClick={() => setShowCommand(true)}
              type="button"
              style={{ width: '100%' }}
            >
              Copy Command
            </button>
            <button
              className="shop-btn"
              onClick={onReset}
              type="button"
              style={{ width: '100%', borderColor: '#DFDED6' }}
            >
              Reset to default
            </button>
          </div>
          <p
            style={{
              margin: '16px 0 0',
              fontSize: 12.5,
              lineHeight: 1.5,
              color: '#8B9794',
            }}
          >
            This is a live preview — changes revert when you close. Use Copy
            Command to apply them for real.
          </p>
        </div>
      </div>

      {showCommand && (
        <CommandModal
          branding={brandingState}
          onClose={() => setShowCommand(false)}
        />
      )}
    </>
  );
}
