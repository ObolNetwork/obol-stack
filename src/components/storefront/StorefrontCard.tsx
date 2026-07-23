'use client';

import { useCallback } from 'react';
import { InlineMarkdown } from './Markdown';
import type { StorefrontCardProps } from './storefront-utils';

interface Props {
  card: StorefrontCardProps;
  onBuy?: (card: StorefrontCardProps) => void;
}

export function StorefrontCard({ card, onBuy }: Props) {
  const handleBuy = useCallback(
    (e: React.MouseEvent) => {
      e.stopPropagation();
      onBuy?.(card);
    },
    [card, onBuy]
  );

  return (
    <article className="shop-card">
      <div className="shop-card-header">
        <div className="shop-card-monogram">{card.initials}</div>
        <div className="shop-card-title-block">
          <h3 className="shop-card-name" title={card.name}>
            {card.name}
          </h3>
          <p className="shop-card-provider">{card.provider}</p>
        </div>
        <span className="shop-card-status">
          <span className="shop-card-status-dot" data-tone={card.statusTone} />
          {card.statusLabel}
        </span>
      </div>

      <InlineMarkdown text={card.blurb} className="shop-card-blurb" />

      {card.tags.length > 0 && (
        <div className="shop-card-tags">
          {card.tags.slice(0, 4).map(tag => (
            <span key={tag} className="shop-tag">
              {tag}
            </span>
          ))}
          {card.tags.length > 4 && (
            <span className="shop-tag">+{card.tags.length - 4}</span>
          )}
        </div>
      )}

      <div className="shop-card-footer">
        <div className="shop-card-price">
          <div className="shop-card-price-line">
            <span className="shop-price-amount">{card.price}</span>
            {card.price !== '—' && (
              <span className="shop-price-symbol">{card.priceSymbol}</span>
            )}
          </div>
          {card.priceUnit && (
            <span className="shop-price-unit">{card.priceUnit}</span>
          )}
        </div>
        <span className="shop-card-chain">{card.chain}</span>
        <button
          className="shop-btn shop-btn-primary shop-btn-sm"
          onClick={handleBuy}
          type="button"
        >
          Buy
        </button>
      </div>
    </article>
  );
}
