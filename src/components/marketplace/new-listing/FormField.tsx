'use client';

import type { ChangeEvent, ReactNode } from 'react';
import { styled } from '@obolnetwork/obol-ui';

/**
 * Form primitives for NewListingDialog: labelled field stack and the
 * affix-capable text input shell.
 */

export const FieldLabel = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
  fontWeight: '$semibold',
});

export const Hint = styled('div', {
  fontSize: '$1',
  color: '$muted',
  lineHeight: 1.45,
});

const FieldStack = styled('div', {
  display: 'flex',
  flexDirection: 'column',
  gap: 6,
});

export interface FormFieldProps {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}

export function FormField({ label, hint, children }: FormFieldProps) {
  return (
    <FieldStack>
      <FieldLabel>{label}</FieldLabel>
      {children}
      {hint ? <Hint>{hint}</Hint> : null}
    </FieldStack>
  );
}

export const InputShell = styled('div', {
  display: 'flex',
  alignItems: 'stretch',
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  overflow: 'hidden',

  '&:focus-within': { borderColor: '$obolGreen' },
});

export const Affix = styled('span', {
  display: 'inline-flex',
  alignItems: 'center',
  padding: '0 10px',
  color: '$muted',
  fontSize: '$2',
  background: '$bg02',

  variants: {
    side: {
      left: { borderRight: '1px solid $stroke' },
      right: { borderLeft: '1px solid $stroke' },
    },
  },
});

const NakedInput = styled('input', {
  flex: 1,
  minWidth: 0,
  background: 'transparent',
  border: 0,
  padding: '9px 12px',
  color: '$light',
  fontSize: '$3',
  fontFamily: 'inherit',
  outline: 'none',

  variants: {
    mono: { true: { fontFamily: 'monospace' } },
  },
});

export interface TextInputProps {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  prefix?: ReactNode;
  suffix?: ReactNode;
  mono?: boolean;
  list?: string;
}

export function TextInput({
  value,
  onChange,
  placeholder,
  prefix,
  suffix,
  mono,
  list,
}: TextInputProps) {
  return (
    <InputShell>
      {prefix ? <Affix side="left">{prefix}</Affix> : null}
      <NakedInput
        mono={mono}
        value={value}
        placeholder={placeholder}
        list={list}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange(e.target.value)
        }
      />
      {suffix ? <Affix side="right">{suffix}</Affix> : null}
    </InputShell>
  );
}
