'use client';

import { useCallback, useState } from 'react';
import type { StorefrontCardProps } from './storefront-utils';

interface Props {
  card: StorefrontCardProps;
  onClose: () => void;
}

export function BuyModal({ card, onClose }: Props) {
  const [copied, setCopied] = useState(false);

  const prompt = `Use my Obol agent to buy "${card.name}" from ${card.provider}.\n\nService ID: ${card.id}\nPrice: ${card.price} ${card.priceSymbol} ${card.priceUnit}\nChain: ${card.chain}`;

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(prompt);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // fallback: select text
    }
  }, [prompt]);

  const handleOverlayClick = useCallback(
    (e: React.MouseEvent) => {
      if (e.target === e.currentTarget) onClose();
    },
    [onClose]
  );

  return (
    <div
      className="shop-overlay"
      onClick={handleOverlayClick}
      role="dialog"
      aria-modal="true"
    >
      <div className="shop-modal">
        <div className="shop-modal-header">
          <h2 className="shop-modal-title">Buy {card.name}</h2>
          <button
            className="shop-modal-close"
            onClick={onClose}
            type="button"
            aria-label="Close"
          >
            <svg width="20" height="20" viewBox="0 0 20 20" fill="none">
              <path
                d="M15 5L5 15M5 5l10 10"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
        <div className="shop-modal-body">
          <p
            style={{ fontSize: 14, color: 'var(--ink-2)', margin: '0 0 12px' }}
          >
            Copy this prompt and paste it into your Obol agent to purchase this
            service:
          </p>
          <div className="shop-code-block">
            <pre>{prompt}</pre>
            <button
              className="shop-code-copy"
              onClick={handleCopy}
              type="button"
            >
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
          <p
            style={{ fontSize: 12, color: 'var(--ink-3)', margin: '12px 0 0' }}
          >
            Or run:{' '}
            <code className="mono" style={{ fontSize: 12 }}>
              obol buy {card.cat === 'api' ? 'http' : 'inference'} {card.id}
            </code>
          </p>
        </div>
      </div>
    </div>
  );
}
