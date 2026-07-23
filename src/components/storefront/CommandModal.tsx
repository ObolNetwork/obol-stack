'use client';

import { useCallback, useState } from 'react';
import type { BrandingState } from './storefront-utils';
import { buildCliCommand, buildClaudeCodePrompt } from './storefront-utils';

interface Props {
  branding: BrandingState;
  onClose: () => void;
}

type Tab = 'cli' | 'claude';

export function CommandModal({ branding, onClose }: Props) {
  const [tab, setTab] = useState<Tab>('cli');
  const [copied, setCopied] = useState(false);

  const cliCmd = buildCliCommand(branding);
  const claudePrompt = buildClaudeCodePrompt(branding);

  const text = tab === 'cli' ? cliCmd : claudePrompt;

  const handleCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // fallback
    }
  }, [text]);

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
          <h2 className="shop-modal-title">Apply Branding</h2>
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
            Copy the command below to apply your branding configuration.
          </p>

          <div style={{ display: 'flex', gap: 4, marginBottom: 12 }}>
            <button
              className="shop-chip"
              data-active={tab === 'cli'}
              onClick={() => {
                setTab('cli');
                setCopied(false);
              }}
              type="button"
            >
              CLI Command
            </button>
            <button
              className="shop-chip"
              data-active={tab === 'claude'}
              onClick={() => {
                setTab('claude');
                setCopied(false);
              }}
              type="button"
            >
              Claude Code
            </button>
          </div>

          <div className="shop-code-block">
            <pre>{text}</pre>
            <button
              className="shop-code-copy"
              onClick={handleCopy}
              type="button"
            >
              {copied ? 'Copied!' : 'Copy'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
