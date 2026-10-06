'use client';

import { useCallback, useState, useRef, useEffect } from 'react';

interface ColorPickerProps {
  label: string;
  value: string;
  onChange: (hex: string) => void;
}

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

function rgbToHex(r: number, g: number, b: number): string {
  return (
    '#' +
    [r, g, b]
      .map(v =>
        Math.max(0, Math.min(255, Math.round(v)))
          .toString(16)
          .padStart(2, '0')
      )
      .join('')
  );
}

function hsvToRgb(h: number, s: number, v: number): [number, number, number] {
  const c = v * s;
  const x = c * (1 - Math.abs(((h / 60) % 2) - 1));
  const m = v - c;
  const [r, g, b] =
    h < 60
      ? [c, x, 0]
      : h < 120
        ? [x, c, 0]
        : h < 180
          ? [0, c, x]
          : h < 240
            ? [0, x, c]
            : h < 300
              ? [x, 0, c]
              : [c, 0, x];
  return [(r + m) * 255, (g + m) * 255, (b + m) * 255];
}

function rgbToHsv(r: number, g: number, b: number): [number, number, number] {
  r /= 255;
  g /= 255;
  b /= 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const d = max - min;
  let h = 0;
  if (d) {
    if (max === r) h = ((g - b) / d) % 6;
    else if (max === g) h = (b - r) / d + 2;
    else h = (r - g) / d + 4;
    h *= 60;
    if (h < 0) h += 360;
  }
  return [h, max ? d / max : 0, max];
}

export function ColorPicker({ label, value, onChange }: ColorPickerProps) {
  const [open, setOpen] = useState(false);
  const [hsv, setHsv] = useState<[number, number, number]>(() => {
    const [r, g, b] = hexToRgb(value);
    return rgbToHsv(r, g, b);
  });
  const [draft, setDraft] = useState(value.replace('#', ''));
  const svRef = useRef<HTMLDivElement>(null);
  const hueRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const [r, g, b] = hexToRgb(value);
    setHsv(rgbToHsv(r, g, b));
    setDraft(value.replace('#', ''));
  }, [value]);

  const emit = useCallback(
    (h: number, s: number, v: number) => {
      const [r, g, b] = hsvToRgb(h, s, v);
      onChange(rgbToHex(r, g, b));
    },
    [onChange]
  );

  const dragSV = useCallback(
    (e: React.MouseEvent) => {
      const box = svRef.current!.getBoundingClientRect();
      const move = (ev: MouseEvent) => {
        const cx = ev.clientX;
        const cy = ev.clientY;
        const s = Math.max(0, Math.min(1, (cx - box.left) / box.width));
        const v = Math.max(0, Math.min(1, 1 - (cy - box.top) / box.height));
        setHsv(([h]) => {
          emit(h, s, v);
          return [h, s, v];
        });
      };
      move(e.nativeEvent);
      const up = () => {
        window.removeEventListener('mousemove', move);
        window.removeEventListener('mouseup', up);
      };
      window.addEventListener('mousemove', move);
      window.addEventListener('mouseup', up);
    },
    [emit]
  );

  const dragHue = useCallback(
    (e: React.MouseEvent) => {
      const box = hueRef.current!.getBoundingClientRect();
      const move = (ev: MouseEvent) => {
        const cx = ev.clientX;
        const h = Math.max(
          0,
          Math.min(360, ((cx - box.left) / box.width) * 360)
        );
        setHsv(([, s, v]) => {
          emit(h, s, v);
          return [h, s, v];
        });
      };
      move(e.nativeEvent);
      const up = () => {
        window.removeEventListener('mousemove', move);
        window.removeEventListener('mouseup', up);
      };
      window.addEventListener('mousemove', move);
      window.addEventListener('mouseup', up);
    },
    [emit]
  );

  const [h, s, v] = hsv;
  const hueColor = rgbToHex(...hsvToRgb(h, 1, 1));

  return (
    <div style={{ position: 'relative', display: 'inline-block' }}>
      {label && <span className="shop-panel-label">{label}</span>}
      <button
        type="button"
        title="Custom colour"
        onClick={() => setOpen(o => !o)}
        style={{
          width: 30,
          height: 30,
          borderRadius: 8,
          cursor: 'pointer',
          padding: 0,
          border: '1px dashed var(--line-strong)',
          boxShadow: 'inset 0 0 0 2px #fff',
          background: value,
          display: 'inline-flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <span
          style={{
            fontSize: 16,
            fontWeight: 600,
            lineHeight: 1,
            color: 'var(--ink)',
            mixBlendMode: 'difference',
            filter: 'invert(1)',
          }}
        >
          +
        </span>
      </button>
      {open && (
        <>
          <div
            style={{ position: 'fixed', inset: 0, zIndex: 5 }}
            onClick={() => setOpen(false)}
          />
          <div
            onClick={e => e.stopPropagation()}
            style={{
              position: 'absolute',
              top: 38,
              right: 0,
              zIndex: 6,
              width: 208,
              background: 'var(--surface)',
              border: '1px solid var(--line-strong)',
              borderRadius: 12,
              padding: 12,
              boxShadow: '0 16px 40px rgba(20,30,28,0.22)',
            }}
          >
            {/* SV field */}
            <div
              ref={svRef}
              onMouseDown={dragSV}
              style={{
                position: 'relative',
                width: '100%',
                height: 140,
                borderRadius: 8,
                cursor: 'crosshair',
                background: `linear-gradient(to top, #000, transparent), linear-gradient(to right, #fff, ${hueColor})`,
              }}
            >
              <div
                style={{
                  position: 'absolute',
                  width: 12,
                  height: 12,
                  borderRadius: '50%',
                  border: '2px solid #fff',
                  boxShadow: '0 0 0 1px rgba(0,0,0,0.4)',
                  transform: 'translate(-50%, -50%)',
                  pointerEvents: 'none',
                  left: `${s * 100}%`,
                  top: `${(1 - v) * 100}%`,
                }}
              />
            </div>
            {/* Hue bar */}
            <div
              ref={hueRef}
              onMouseDown={dragHue}
              style={{
                position: 'relative',
                width: '100%',
                height: 14,
                borderRadius: 999,
                marginTop: 12,
                cursor: 'pointer',
                background:
                  'linear-gradient(to right, #f00 0%, #ff0 17%, #0f0 33%, #0ff 50%, #00f 67%, #f0f 83%, #f00 100%)',
              }}
            >
              <div
                style={{
                  position: 'absolute',
                  top: '50%',
                  width: 16,
                  height: 16,
                  borderRadius: '50%',
                  background: 'var(--surface)',
                  border: '1px solid rgba(0,0,0,0.3)',
                  boxShadow: '0 1px 3px rgba(0,0,0,0.3)',
                  transform: 'translate(-50%, -50%)',
                  pointerEvents: 'none',
                  left: `${(h / 360) * 100}%`,
                }}
              />
            </div>
            {/* Hex input */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 6,
                marginTop: 12,
              }}
            >
              <span
                style={{
                  width: 20,
                  height: 20,
                  borderRadius: 5,
                  background: value,
                  border: '1px solid var(--line-strong)',
                  flexShrink: 0,
                }}
              />
              <span
                className="mono"
                style={{ fontSize: 13, color: 'var(--ink-3)' }}
              >
                #
              </span>
              <input
                className="mono"
                value={draft}
                spellCheck={false}
                maxLength={6}
                onChange={e => {
                  const t = e.target.value.replace(/[^0-9a-fA-F]/g, '');
                  setDraft(t);
                  if (t.length === 3 || t.length === 6) onChange('#' + t);
                }}
                onBlur={() => setDraft(value.replace('#', ''))}
                onKeyDown={e => {
                  if (e.key === 'Enter') (e.target as HTMLInputElement).blur();
                }}
                style={{
                  flex: 1,
                  minWidth: 0,
                  border: '1px solid var(--line-strong)',
                  background: 'var(--bg)',
                  borderRadius: 6,
                  padding: '5px 8px',
                  fontSize: 13,
                  color: 'var(--ink)',
                  textTransform: 'uppercase',
                  letterSpacing: '0.04em',
                }}
              />
            </div>
          </div>
        </>
      )}
    </div>
  );
}
