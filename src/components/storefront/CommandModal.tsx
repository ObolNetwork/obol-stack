'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import type { BrandingField, BrandingState } from './storefront-utils';
import {
  buildCliCommand,
  buildClaudeCodePrompt,
  collapseDataUrisForDisplay,
} from './storefront-utils';

interface Props {
  branding: BrandingState;
  changedFields: ReadonlySet<BrandingField>;
  onClose: () => void;
}

type Tab = 'cli' | 'claude';

async function copyToClipboard(text: string): Promise<void> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  // The in-cluster dashboard is commonly served over http://obol.stack,
  // where the modern Clipboard API is unavailable. execCommand remains the
  // only broadly-supported synchronous fallback for a user-initiated click.
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, text.length);
  const copied = document.execCommand('copy');
  textarea.remove();
  if (!copied) throw new Error('clipboard copy was rejected');
}

export function CommandModal({ branding, changedFields, onClose }: Props) {
  const [tab, setTab] = useState<Tab>('cli');
  const [copied, setCopied] = useState(false);
  const [copyFailed, setCopyFailed] = useState(false);
  const closeRef = useRef<HTMLButtonElement>(null);

  // Focus the dialog on open and close on Escape.
  useEffect(() => {
    closeRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const cliCmd = buildCliCommand(branding, changedFields);
  const claudePrompt = buildClaudeCodePrompt(branding, changedFields);
  const text = tab === 'cli' ? cliCmd : claudePrompt;
  const displayText = collapseDataUrisForDisplay(text);
  const isDisplayCollapsed = displayText !== text;

  const handleCopy = useCallback(async () => {
    try {
      await copyToClipboard(text);
      setCopied(true);
      setCopyFailed(false);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setCopied(false);
      setCopyFailed(true);
    }
  }, [text]);

  return (
    <div
      className="shop-overlay"
      onClick={e => {
        if (e.target === e.currentTarget) onClose();
      }}
      role="dialog"
      aria-modal="true"
      aria-labelledby="command-modal-title"
    >
      <div className="shop-modal">
        <div className="shop-modal-header">
          <h2 className="shop-modal-title" id="command-modal-title">
            Apply branding
          </h2>
          <button
            ref={closeRef}
            className="shop-modal-close"
            onClick={onClose}
            type="button"
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <div className="shop-modal-body">
          <p
            style={{ fontSize: 14, color: 'var(--ink-2)', margin: '0 0 12px' }}
          >
            Run this on the operator host (with your cluster kubeconfig). It
            patches the storefront profile via <code>obol sell info set</code> —
            the public storefront picks it up from{' '}
            <code>/api/services.json</code>.
          </p>

          <div style={{ display: 'flex', gap: 4, marginBottom: 12 }}>
            <button
              className="shop-chip"
              data-active={tab === 'cli'}
              onClick={() => {
                setTab('cli');
                setCopied(false);
                setCopyFailed(false);
              }}
              type="button"
            >
              CLI command
            </button>
            <button
              className="shop-chip"
              data-active={tab === 'claude'}
              onClick={() => {
                setTab('claude');
                setCopied(false);
                setCopyFailed(false);
              }}
              type="button"
            >
              Claude Code
            </button>
          </div>

          <div className="shop-code-block">
            <pre>{displayText}</pre>
            <button
              className="shop-code-copy"
              onClick={handleCopy}
              type="button"
              title={
                isDisplayCollapsed ? 'Copies the complete data URI' : undefined
              }
            >
              {copied ? 'Copied!' : copyFailed ? 'Copy failed' : 'Copy'}
            </button>
          </div>
          {isDisplayCollapsed && (
            <p
              style={{
                fontSize: 12,
                color: 'var(--ink-2)',
                margin: '8px 0 0',
              }}
            >
              Inline image shortened for display. Copy still includes the
              complete data URI.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}
