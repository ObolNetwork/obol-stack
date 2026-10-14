'use client';

import React, { useState, MouseEvent } from 'react';
import { theme } from '@obolnetwork/obol-ui';
import { CopyIcon, CheckIcon } from '@radix-ui/react-icons';
import { Button } from '@/components/atoms/Button';

export interface CopyButtonProps {
  text: string;
  title?: string;
}

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
    <Button
      type="button"
      variant="bare"
      size="sm"
      iconOnly
      css={{ flexShrink: 0 }}
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
    </Button>
  );
}
