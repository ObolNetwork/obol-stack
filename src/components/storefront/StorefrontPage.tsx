'use client';

import { useState, useMemo, useCallback, useRef, useEffect } from 'react';
import { ConnectButton } from '@rainbow-me/rainbowkit';
import { useDisconnect } from 'wagmi';

import { useMarketplaceCatalog } from '@/hooks/useMarketplaceCatalog';
import { isStackUnreachable } from '@/lib/marketplace/errors';
import type { MarketplaceFilters } from '@/lib/marketplace/types';

import { ObolWordmark } from './ObolWordmark';
import { StorefrontCard } from './StorefrontCard';
import { BuyModal } from './BuyModal';
import { BrandingPanel } from './BrandingPanel';
import type {
  StorefrontCategory,
  StorefrontCardProps as CardProps,
} from './storefront-utils';
import {
  toCardProps,
  countByCategory,
  filterByCategory,
  filterBySearch,
  THEMES,
  applyBrand,
  applyInk,
  applyCardColor,
  clearThemeOverrides,
  type BrandingState,
} from './storefront-utils';

const FILTERS: MarketplaceFilters = {
  registry: 'obol',
  sort: 'recent',
  pageSize: 100,
};

const CATEGORIES: { key: StorefrontCategory; label: string }[] = [
  { key: 'all', label: 'All services' },
  { key: 'agent', label: 'Agents' },
  { key: 'api', label: 'APIs' },
  { key: 'demo', label: 'Demo' },
];

function truncateAddr(addr: string): string {
  return addr.slice(0, 6) + '…' + addr.slice(-4);
}

function StorefrontWallet() {
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const { disconnect } = useDisconnect();

  useEffect(() => {
    if (!menuOpen) return;
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setMenuOpen(false);
      }
    };
    document.addEventListener('mousedown', handleClick);
    return () => document.removeEventListener('mousedown', handleClick);
  }, [menuOpen]);

  return (
    <ConnectButton.Custom>
      {({
        account,
        chain,
        openConnectModal,
        openChainModal,
        authenticationStatus,
        mounted,
      }: {
        account?: {
          address: string;
          displayName: string;
          ensName?: string;
        } | null;
        chain?: { id: number; name?: string; unsupported?: boolean } | null;
        openConnectModal: () => void;
        openChainModal: () => void;
        authenticationStatus?: 'loading' | 'unauthenticated' | 'authenticated';
        mounted: boolean;
      }) => {
        const ready = mounted && authenticationStatus !== 'loading';
        const connected =
          ready &&
          account &&
          chain &&
          (!authenticationStatus || authenticationStatus === 'authenticated');

        if (!ready) return null;

        if (!connected) {
          return (
            <button
              className="shop-btn shop-btn-primary"
              onClick={openConnectModal}
              type="button"
            >
              Connect wallet
            </button>
          );
        }

        if (chain.unsupported) {
          return (
            <button
              className="shop-btn"
              onClick={openChainModal}
              type="button"
              style={{ borderColor: '#ef4444', color: '#ef4444' }}
            >
              Wrong network
            </button>
          );
        }

        return (
          <div style={{ position: 'relative' }} ref={menuRef}>
            <button
              className="shop-btn"
              onClick={() => setMenuOpen(v => !v)}
              type="button"
              style={{
                borderColor: 'var(--brand)',
                display: 'inline-flex',
                alignItems: 'center',
                gap: 8,
                paddingLeft: 10,
                paddingRight: 12,
              }}
            >
              {/* Chain dot */}
              <span
                style={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 5,
                  fontSize: 12,
                  fontWeight: 600,
                  color: 'var(--brand)',
                }}
              >
                <span
                  style={{
                    width: 7,
                    height: 7,
                    borderRadius: 999,
                    background: 'var(--brand)',
                  }}
                />
                {chain.name}
              </span>
              <span
                style={{
                  width: 1,
                  height: 16,
                  background: 'var(--line-strong)',
                }}
              />
              <span style={{ fontWeight: 600, color: 'var(--ink)' }}>
                {account.displayName || truncateAddr(account.address)}
              </span>
            </button>

            {menuOpen && (
              <div
                style={{
                  position: 'absolute',
                  top: 'calc(100% + 6px)',
                  right: 0,
                  background: 'var(--surface)',
                  border: '1px solid var(--line-strong)',
                  borderRadius: 12,
                  padding: 16,
                  minWidth: 220,
                  boxShadow: '0 12px 32px rgba(20,30,28,0.18)',
                  zIndex: 30,
                }}
              >
                {/* Address */}
                <div style={{ marginBottom: 12 }}>
                  <div
                    className="mono"
                    style={{
                      fontSize: 11,
                      textTransform: 'uppercase',
                      letterSpacing: '0.08em',
                      color: 'var(--ink-3)',
                      marginBottom: 4,
                    }}
                  >
                    Wallet
                  </div>
                  <div
                    className="mono"
                    style={{
                      fontSize: 13,
                      color: 'var(--ink)',
                      wordBreak: 'break-all',
                    }}
                  >
                    {account.address}
                  </div>
                </div>

                {/* Chain */}
                <div style={{ marginBottom: 16 }}>
                  <div
                    className="mono"
                    style={{
                      fontSize: 11,
                      textTransform: 'uppercase',
                      letterSpacing: '0.08em',
                      color: 'var(--ink-3)',
                      marginBottom: 4,
                    }}
                  >
                    Network
                  </div>
                  <button
                    type="button"
                    onClick={() => {
                      openChainModal();
                      setMenuOpen(false);
                    }}
                    style={{
                      background: 'none',
                      border: 'none',
                      padding: 0,
                      cursor: 'pointer',
                      display: 'flex',
                      alignItems: 'center',
                      gap: 6,
                      fontSize: 13,
                      fontWeight: 600,
                      color: 'var(--brand)',
                      fontFamily: 'inherit',
                    }}
                  >
                    <span
                      style={{
                        width: 7,
                        height: 7,
                        borderRadius: 999,
                        background: 'var(--brand)',
                      }}
                    />
                    {chain.name}
                    <span style={{ fontSize: 11, color: 'var(--ink-3)' }}>
                      switch
                    </span>
                  </button>
                </div>

                {/* Disconnect */}
                <button
                  className="shop-btn"
                  type="button"
                  onClick={() => {
                    disconnect();
                    setMenuOpen(false);
                  }}
                  style={{
                    width: '100%',
                    borderColor: 'var(--line-strong)',
                    justifyContent: 'center',
                  }}
                >
                  Disconnect
                </button>
              </div>
            )}
          </div>
        );
      }}
    </ConnectButton.Custom>
  );
}

export function StorefrontPage() {
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState<StorefrontCategory>('all');
  const [buyCard, setBuyCard] = useState<CardProps | null>(null);
  const [showBranding, setShowBranding] = useState(false);
  const [x402Open, setX402Open] = useState(false);

  // Branding state — lifted so header/banner respond to panel changes
  const [displayName, setDisplayName] = useState('Agent Services');
  const [tagline, setTagline] = useState(
    'Unlock Agent and API services with digital payments.'
  );
  const [logo, setLogo] = useState<string | null>(null);
  const [theme, setTheme] = useState<BrandingState['theme']>('light');
  const [accent, setAccentState] = useState('#2FE4AB');
  const [bg, setBgState] = useState('#F4F4F1');
  const [text, setTextState] = useState('#16211F');
  const [card, setCardState] = useState('#FFFFFF');

  const { data, isLoading, isError, error, refetch } =
    useMarketplaceCatalog(FILTERS);

  const allCards = useMemo(() => {
    if (!data) return [];
    return data.items.map(toCardProps);
  }, [data]);

  const counts = useMemo(() => countByCategory(allCards), [allCards]);

  const visibleCards = useMemo(() => {
    let items = filterByCategory(allCards, category);
    items = filterBySearch(items, search);
    return items;
  }, [allCards, category, search]);

  const handleBuy = useCallback((c: CardProps) => {
    setBuyCard(c);
  }, []);

  // ── Branding callbacks ──
  const handleThemeChange = useCallback((key: BrandingState['theme']) => {
    const t = THEMES[key];
    setTheme(key);
    setAccentState(t.accent);
    setBgState(t.bg);
    setTextState(t.text);
    applyBrand(t.accent, t.bg);
    applyInk(t.text);
    // Read derived card colour back from the computed style
    setCardState(
      getComputedStyle(document.documentElement)
        .getPropertyValue('--card')
        .trim()
    );
  }, []);

  const handleAccentChange = useCallback((c: string) => {
    setAccentState(c);
    applyBrand(c, null);
  }, []);

  const handleBgChange = useCallback(
    (c: string) => {
      setBgState(c);
      applyBrand(null, c);
      applyInk(text);
    },
    [text]
  );

  const handleTextChange = useCallback((c: string) => {
    setTextState(c);
    applyInk(c);
  }, []);

  const handleCardChange = useCallback((c: string) => {
    setCardState(c);
    applyCardColor(c);
  }, []);

  const resetBrand = useCallback(() => {
    setAccentState('#2FE4AB');
    setBgState('#F4F4F1');
    setTextState('#16211F');
    setCardState('#FFFFFF');
    setLogo(null);
    setTheme('light');
    clearThemeOverrides();
  }, []);

  const closeBranding = useCallback(() => {
    resetBrand();
    setDisplayName('Agent Services');
    setTagline('Unlock Agent and API services with digital payments.');
    setShowBranding(false);
  }, [resetBrand]);

  const unreachable = isError && isStackUnreachable(error);

  return (
    <div className="shop-page">
      {/* Preview mode badge — fixed to top center when panel is open */}
      {showBranding && (
        <div
          style={{
            position: 'fixed',
            top: 16,
            left: '50%',
            transform: 'translateX(-50%)',
            zIndex: 55,
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            background: '#16211F',
            color: '#F4F4F1',
            fontSize: 12.5,
            fontWeight: 600,
            padding: '8px 15px',
            borderRadius: 999,
            boxShadow: '0 6px 20px rgba(20,30,28,0.28)',
          }}
        >
          <span
            style={{
              width: 7,
              height: 7,
              borderRadius: 999,
              background: '#2FE4AB',
            }}
          />
          Preview mode — changes revert when you close
        </div>
      )}

      {/* Header */}
      <header className="shop-header">
        <div className="shop-header-left">
          {logo ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img
              src={logo}
              alt="Storefront logo"
              style={{
                height: 28,
                maxWidth: 180,
                objectFit: 'contain',
                display: 'block',
              }}
            />
          ) : (
            <ObolWordmark height={20} />
          )}
          <span
            style={{ width: 1, height: 22, background: 'var(--line-strong)' }}
          />
          <span
            className="mono"
            style={{
              fontSize: 12,
              letterSpacing: '0.12em',
              textTransform: 'uppercase',
              color: 'var(--ink-2)',
            }}
          >
            Store
          </span>
        </div>
        <div className="shop-header-right">
          <div className="shop-search" style={{ maxWidth: 320, width: '34vw' }}>
            <svg width="15" height="15" viewBox="0 0 16 16" fill="none">
              <circle
                cx="7"
                cy="7"
                r="4.5"
                stroke="currentColor"
                strokeWidth="1.4"
              />
              <path
                d="M10.5 10.5 14 14"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
            <input
              type="text"
              placeholder="Search services..."
              value={search}
              onChange={e => setSearch(e.target.value)}
              aria-label="Search services"
              style={{
                border: 0,
                background: 'transparent',
                outline: 'none',
                fontSize: 14,
                color: 'var(--ink)',
                width: '100%',
                fontFamily: 'inherit',
              }}
            />
          </div>
          <button
            className="shop-btn"
            onClick={() => setShowBranding(true)}
            type="button"
            style={{
              borderColor: 'var(--line-strong)',
              display: 'inline-flex',
              alignItems: 'center',
              gap: 7,
            }}
          >
            <svg width="16" height="16" viewBox="0 0 20 20" fill="none">
              <path
                d="M13.5 3.8l2.7 2.7L7.4 15.3l-3.4.7.7-3.4 8.8-8.8z"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinejoin="round"
              />
              <path
                d="M12.2 5.1l2.7 2.7"
                stroke="currentColor"
                strokeWidth="1.6"
              />
            </svg>
            Customise
          </button>
          <StorefrontWallet />
        </div>
      </header>

      {/* Banner */}
      <div className="shop-banner">
        <h1>{displayName}</h1>
        <p>{tagline}</p>
      </div>

      {/* Stack unreachable alert */}
      {unreachable && (
        <div style={{ maxWidth: 960, margin: '0 auto', padding: '0 32px' }}>
          <div className="shop-alert">
            <span className="shop-alert-icon">!</span>
            <span className="shop-alert-text">
              Obol Stack unreachable — catalog may be stale or empty.
            </span>
            <button
              className="shop-alert-action"
              onClick={() => refetch()}
              type="button"
            >
              Retry
            </button>
          </div>
        </div>
      )}

      {/* Toolbar: chips */}
      <div style={{ maxWidth: 960, margin: '0 auto', padding: '0 32px' }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '12px 0',
            borderBottom: '1px solid var(--line-strong)',
            flexWrap: 'wrap',
          }}
        >
          {CATEGORIES.map(({ key, label }) => (
            <button
              key={key}
              className="shop-chip"
              data-active={category === key}
              onClick={() => setCategory(key)}
              type="button"
            >
              {label}
              <span className="shop-chip-count">{counts[key]}</span>
            </button>
          ))}
          <span style={{ flex: 1 }} />
          <span
            className="mono"
            style={{ fontSize: 12, color: 'var(--ink-3)' }}
          >
            {visibleCards.length}{' '}
            {visibleCards.length === 1 ? 'service' : 'services'}
          </span>
        </div>
      </div>

      {/* Main content */}
      <div className="shop-main">
        {isLoading ? (
          <div className="shop-grid">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="shop-skeleton shop-skeleton-card" />
            ))}
          </div>
        ) : visibleCards.length === 0 && search ? (
          <div className="shop-empty">
            <h3>No services match your search</h3>
            <p>Try a different search term or clear the filter.</p>
            <button
              className="shop-btn"
              onClick={() => {
                setSearch('');
                setCategory('all');
              }}
              type="button"
              style={{ borderColor: 'var(--line-strong)' }}
            >
              Clear filters
            </button>
          </div>
        ) : visibleCards.length === 0 ? (
          <div className="shop-empty">
            <h3>No services listed yet</h3>
            <p>
              Publish your first service with the{' '}
              <code className="mono">obol sell</code> CLI.
            </p>
            <div
              className="shop-code-block"
              style={{ display: 'inline-block', textAlign: 'left' }}
            >
              <pre>obol sell inference</pre>
            </div>
          </div>
        ) : (
          <div className="shop-grid">
            {visibleCards.map(c => (
              <StorefrontCard key={c.id} card={c} onBuy={handleBuy} />
            ))}
          </div>
        )}

        {/* x402 callout */}
        {visibleCards.length > 0 && (
          <div
            className="shop-x402-callout"
            style={{ flexDirection: 'column', cursor: 'pointer' }}
            onClick={() => setX402Open(v => !v)}
          >
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                gap: 20,
                width: '100%',
                flexWrap: 'wrap',
              }}
            >
              <div
                style={{
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 3,
                  flex: 1,
                }}
              >
                <span
                  style={{
                    fontSize: 15.5,
                    fontWeight: 700,
                    color: 'var(--ink)',
                  }}
                >
                  New to x402 payments?
                </span>
                <span style={{ fontSize: 13.5, color: 'var(--ink-2)' }}>
                  Learn how agents pay for services inline over HTTP.
                </span>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <a
                  href="https://www.x402.org"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="shop-btn shop-btn-primary"
                  style={{ whiteSpace: 'nowrap', textDecoration: 'none' }}
                  onClick={e => e.stopPropagation()}
                >
                  How x402 works &rarr;
                </a>
                <span
                  style={{
                    width: 28,
                    height: 28,
                    borderRadius: 8,
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    transition: 'transform 200ms',
                    transform: x402Open ? 'rotate(180deg)' : 'rotate(0deg)',
                    color: 'var(--ink-2)',
                  }}
                >
                  <svg width="14" height="14" viewBox="0 0 16 16" fill="none">
                    <path
                      d="M4 6l4 4 4-4"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                </span>
              </div>
            </div>
            {x402Open && (
              <div
                style={{
                  width: '100%',
                  paddingTop: 16,
                  borderTop: '1px solid var(--brand)',
                  marginTop: 16,
                }}
              >
                <p
                  style={{
                    fontSize: 14,
                    lineHeight: 1.6,
                    color: 'var(--ink-2)',
                    margin: 0,
                  }}
                >
                  A request without payment returns HTTP 402 with the price and
                  a payment recipe. The buyer (or their library) signs an
                  off-chain authorization, retries the request with an{' '}
                  <code
                    className="mono"
                    style={{
                      fontSize: 13,
                      background: 'var(--bg)',
                      padding: '1px 5px',
                      borderRadius: 4,
                      border: '1px solid var(--line-strong)',
                    }}
                  >
                    X-PAYMENT
                  </code>{' '}
                  header, and the seller&#8217;s facilitator settles on-chain.
                  No gas needed — the seller covers settlement.
                </p>
                <p
                  style={{
                    fontSize: 13,
                    lineHeight: 1.5,
                    color: 'var(--ink-3)',
                    margin: '10px 0 0',
                  }}
                >
                  Useful for: data feeds, AI inference, subscriptions, digital
                  purchases, and agent-to-agent commerce.
                </p>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Footer */}
      <footer
        className="shop-footer"
        style={{ justifyContent: 'space-between', flexWrap: 'wrap' }}
      >
        <ObolWordmark height={18} />
        <div style={{ display: 'flex', gap: 24, flexWrap: 'wrap' }}>
          <a
            href="/skill.md"
            target="_blank"
            rel="noopener noreferrer"
            className="shop-link mono"
          >
            /skill.md &#8599;
          </a>
          <a
            href="/.well-known/agent-registration.json"
            target="_blank"
            rel="noopener noreferrer"
            className="shop-link mono"
          >
            /.well-known/agent-registration.json &#8599;
          </a>
        </div>
      </footer>

      {/* Modals */}
      {buyCard && <BuyModal card={buyCard} onClose={() => setBuyCard(null)} />}

      {showBranding && (
        <BrandingPanel
          onClose={closeBranding}
          onReset={resetBrand}
          displayName={displayName}
          tagline={tagline}
          onDisplayNameChange={setDisplayName}
          onTaglineChange={setTagline}
          logo={logo}
          onLogoChange={setLogo}
          accent={accent}
          onAccentChange={handleAccentChange}
          bg={bg}
          onBgChange={handleBgChange}
          text={text}
          onTextChange={handleTextChange}
          card={card}
          onCardChange={handleCardChange}
          theme={theme}
          onThemeChange={handleThemeChange}
        />
      )}
    </div>
  );
}
