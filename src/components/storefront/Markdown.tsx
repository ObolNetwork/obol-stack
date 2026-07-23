'use client';

import type { ReactNode } from 'react';

/**
 * Lightweight inline markdown renderer — handles bold, italic, code,
 * links, headings, and unordered lists. No external dependencies.
 */

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function renderInline(text: string): string {
  let out = escapeHtml(text);
  // bold **text** or __text__
  out = out.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  out = out.replace(/__(.+?)__/g, '<strong>$1</strong>');
  // italic *text* or _text_
  out = out.replace(/\*(.+?)\*/g, '<em>$1</em>');
  out = out.replace(/(?<!\w)_(.+?)_(?!\w)/g, '<em>$1</em>');
  // inline code `text`
  out = out.replace(/`(.+?)`/g, '<code>$1</code>');
  // links [text](url)
  out = out.replace(
    /\[([^\]]+)\]\(([^)]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>'
  );
  return out;
}

function markdownToHtml(md: string): string {
  const lines = md.split('\n');
  const htmlParts: string[] = [];
  let inList = false;

  for (const line of lines) {
    const trimmed = line.trim();

    // headings
    if (trimmed.startsWith('### ')) {
      if (inList) {
        htmlParts.push('</ul>');
        inList = false;
      }
      htmlParts.push(`<strong>${renderInline(trimmed.slice(4))}</strong>`);
      continue;
    }
    if (trimmed.startsWith('## ')) {
      if (inList) {
        htmlParts.push('</ul>');
        inList = false;
      }
      htmlParts.push(`<strong>${renderInline(trimmed.slice(3))}</strong>`);
      continue;
    }
    if (trimmed.startsWith('# ')) {
      if (inList) {
        htmlParts.push('</ul>');
        inList = false;
      }
      htmlParts.push(`<strong>${renderInline(trimmed.slice(2))}</strong>`);
      continue;
    }

    // unordered list
    if (/^[-*]\s/.test(trimmed)) {
      if (!inList) {
        htmlParts.push('<ul>');
        inList = true;
      }
      htmlParts.push(`<li>${renderInline(trimmed.slice(2))}</li>`);
      continue;
    }

    // end list if we had one
    if (inList && !trimmed) {
      htmlParts.push('</ul>');
      inList = false;
      continue;
    }

    // empty line
    if (!trimmed) continue;

    // paragraph
    if (inList) {
      htmlParts.push('</ul>');
      inList = false;
    }
    htmlParts.push(`<p>${renderInline(trimmed)}</p>`);
  }

  if (inList) htmlParts.push('</ul>');
  return htmlParts.join('');
}

export function InlineMarkdown({
  text,
  className,
}: {
  text: string;
  className?: string;
}): ReactNode {
  const html = markdownToHtml(text);
  return (
    <div className={className} dangerouslySetInnerHTML={{ __html: html }} />
  );
}
