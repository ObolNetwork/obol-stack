'use client';

import React, { useState, MouseEvent } from 'react';
import { styled, theme } from '@obolnetwork/obol-ui';
import { CopyIcon, CheckIcon } from '@radix-ui/react-icons';

export interface CopyButtonProps {
  text: string;
  title?: string;
}

const CopyBtn = styled('button', {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: 22,
  height: 22,
  padding: 0,
  border: '1px solid $stroke',
  borderRadius: '$2',
  background: 'transparent',
  cursor: 'pointer',
  color: '$body',
  transition:
    'color 150ms ease, border-color 150ms ease, background 150ms ease',
  '&:hover': {
    color: '$light',
    borderColor: '$body',
    background: '$bg05',
  },
});

export function CopyButton({ text, title = 'Copy' }: CopyButtonProps) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async (e: MouseEvent) => {
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // Fallback for insecure contexts (e.g. http://obol.stack on Firefox)
      const textarea = document.createElement('textarea');
      textarea.value = text;
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      document.body.appendChild(textarea);
      textarea.select();
      document.execCommand('copy');
      document.body.removeChild(textarea);
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };

  return (
    <CopyBtn
      type="button"
      onClick={handleCopy}
      title={title}
      aria-label={title}
    >
      {copied ? (
        <CheckIcon
          width={13}
          height={13}
          color={theme.colors.obolGreen.value}
        />
      ) : (
        <CopyIcon width={13} height={13} />
      )}
    </CopyBtn>
  );
}
