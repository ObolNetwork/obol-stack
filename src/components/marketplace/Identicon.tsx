'use client';

/**
 * Geometric identicon — 4x4 symmetric grid of squares filled or empty by
 * hashing the seed. Mirrors the design canvas Identicon (Services.html)
 * pattern: deterministic from the wallet so the same provider always
 * renders the same glyph, no network call, no canvas, no extra dependency.
 */
export interface IdenticonProps {
  seed: string;
  size?: number;
  rounded?: number;
}

const PALETTE = [
  '#2FE4AB', // obolGreen
  '#3CD2DD', // create / base cyan
  '#9167E4', // coordinate purple
  '#FABA5A', // obolGold
  '#B6EA5C', // obolGreenLight
  '#BB99FF', // coordinate hover
];

function hashSeed(seed: string): number {
  let h = 2166136261 >>> 0; // FNV-1a 32-bit
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

export function Identicon({ seed, size = 38, rounded = 8 }: IdenticonProps) {
  const hash = hashSeed(seed || 'unknown');
  const fg = PALETTE[hash % PALETTE.length];
  // 4 columns × 4 rows, mirror columns 2/3 ← 1/0 so the glyph stays
  // symmetric. 16 cells / mirrored = 8 unique bits.
  const cells: boolean[] = [];
  for (let row = 0; row < 4; row++) {
    const rowBits = [
      ((hash >> (row * 3)) & 1) === 1,
      ((hash >> (row * 3 + 1)) & 1) === 1,
    ];
    cells.push(rowBits[0], rowBits[1], rowBits[1], rowBits[0]);
  }
  const cellSize = size / 4;

  return (
    <svg
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={`identicon for ${seed.slice(0, 10)}`}
      style={{ flexShrink: 0 }}
    >
      <rect width={size} height={size} rx={rounded} fill="#0A1416" />
      {cells.map((on, i) => {
        if (!on) return null;
        const col = i % 4;
        const row = Math.floor(i / 4);
        return (
          <rect
            key={i}
            x={col * cellSize}
            y={row * cellSize}
            width={cellSize}
            height={cellSize}
            fill={fg}
            opacity={0.92}
          />
        );
      })}
    </svg>
  );
}

export function shortAddress(addr: string): string {
  if (!addr) return '';
  if (addr.length <= 10) return addr;
  return `${addr.slice(0, 6)}…${addr.slice(-4)}`;
}
