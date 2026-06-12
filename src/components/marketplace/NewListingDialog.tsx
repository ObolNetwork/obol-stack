'use client';

import type {
  ChangeEvent,
  MouseEvent as ReactMouseEvent,
  ReactNode,
} from 'react';
import { useEffect, useMemo, useState } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { ChainIcon } from '@/components/atoms/ChainIcon';
import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import {
  CLIENT_SELL_ASSET_ENV,
  isSellTokenSupportedOnChain,
  reconcileSellPaymentSelection,
  resolveSellAsset,
  type SellPaymentChain,
  type SellPaymentToken,
} from '@/lib/services/sell-assets';
import { DEFAULT_AGENT_ID } from '@/lib/services/agent-runtime';
import { useOperatorWallets } from '@/hooks/useOperatorWallets';
import type { ChainName } from '@/types/dashboard';

export interface NewListingDialogProps {
  open: boolean;
  onClose: () => void;
}

type ServiceType = 'inference' | 'route';
type PaymentToken = SellPaymentToken;
type PriceUnit = 'req' | 'mtok' | 'hour';
type Visibility = 'local' | 'erc8004';
type ChainKey = SellPaymentChain;

const Mono = styled('span', { fontFamily: 'monospace' });

const TOKEN_PREFIX: Record<PaymentToken, string> = {
  USDC: '$',
  OBOL: 'Ⓞ',
};

const CHAIN_OPTIONS: Array<{
  k: ChainKey;
  label: string;
  icon: ChainName;
}> = [
  { k: 'base', label: 'Base', icon: 'base' },
  { k: 'base-sepolia', label: 'Base Sepolia', icon: 'base-sepolia' },
  { k: 'mainnet', label: 'Mainnet', icon: 'mainnet' },
];

// ── Styled shell ────────────────────────────────────────────────────────

const Backdrop = styled('div', {
  position: 'fixed',
  inset: 0,
  background: 'rgba(4, 8, 9, 0.55)',
  backdropFilter: 'blur(2px)',
  zIndex: 80,
  display: 'flex',
  alignItems: 'flex-start',
  justifyContent: 'center',
  padding: '48px 24px 24px',
  overflowY: 'auto',
});

const Panel = styled('div', {
  width: 720,
  maxWidth: 'calc(100vw - 48px)',
  maxHeight: '92vh',
  background: '$bg02',
  border: '1px solid $stroke',
  borderRadius: '$4',
  boxShadow: '0 30px 90px rgba(0,0,0,0.55)',
  overflow: 'hidden',
  display: 'flex',
  flexDirection: 'column',
});

const Header = styled('header', {
  padding: '$md $lg',
  display: 'flex',
  alignItems: 'center',
  gap: '$xs',
  borderBottom: '1px solid $stroke',
});

const HeaderIcon = styled('div', {
  width: 32,
  height: 32,
  borderRadius: 8,
  background: '$obolMidGreen',
  color: '$obolGreen',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
});

const Eyebrow = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$muted',
  fontWeight: '$semibold',
  marginBottom: 2,
});

const HeaderTitle = styled('div', {
  fontSize: '$5',
  fontWeight: '$semibold',
  color: '$light',
  letterSpacing: '-0.005em',
});

const CloseBtn = styled('button', {
  background: 'transparent',
  border: 0,
  color: '$body',
  cursor: 'pointer',
  padding: 6,
  borderRadius: 4,
  '&:hover': { color: '$light' },
});

const Body = styled('div', {
  padding: '$md $lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  overflowY: 'auto',
});

const Footer = styled('footer', {
  padding: '$xs $lg',
  borderTop: '1px solid $stroke',
  display: 'flex',
  alignItems: 'center',
  gap: 10,
  background: '$bg02',
});

// ── Form primitives ─────────────────────────────────────────────────────

const FieldLabel = styled('div', {
  fontSize: '$1',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
  color: '$body',
  fontWeight: '$semibold',
});

const Hint = styled('div', {
  fontSize: '$1',
  color: '$muted',
  lineHeight: 1.45,
});

const FieldStack = styled('div', {
  display: 'flex',
  flexDirection: 'column',
  gap: 6,
});

function FormField({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: ReactNode;
  children: React.ReactNode;
}) {
  return (
    <FieldStack>
      <FieldLabel>{label}</FieldLabel>
      {children}
      {hint ? <Hint>{hint}</Hint> : null}
    </FieldStack>
  );
}

const SegmentedRow = styled('div', {
  display: 'inline-flex',
  padding: 3,
  gap: 2,
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  width: '100%',
});

const SegmentedButton = styled('button', {
  flex: 1,
  padding: '8px 10px',
  border: 0,
  borderRadius: '$1',
  cursor: 'pointer',
  fontFamily: 'inherit',
  fontSize: '$2',
  background: 'transparent',
  color: '$body',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 6,
  transition: 'background 120ms, color 120ms',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        fontWeight: '$semibold',
      },
      false: { fontWeight: '$medium' },
    },
    unavailable: {
      true: {
        cursor: 'not-allowed',
        opacity: 0.5,
      },
    },
  },
});

function Segmented<T extends string>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: Array<{ k: T; label: string; disabled?: boolean; title?: string }>;
  onChange: (v: T) => void;
}) {
  return (
    <SegmentedRow role="tablist">
      {options.map(opt => (
        <SegmentedButton
          key={opt.k}
          type="button"
          role="tab"
          aria-selected={opt.k === value}
          active={opt.k === value}
          unavailable={opt.disabled}
          disabled={opt.disabled}
          title={opt.title}
          onClick={() => {
            if (!opt.disabled) onChange(opt.k);
          }}
        >
          {opt.label}
        </SegmentedButton>
      ))}
    </SegmentedRow>
  );
}

const InputShell = styled('div', {
  display: 'flex',
  alignItems: 'stretch',
  background: '$bg03',
  border: '1px solid $stroke',
  borderRadius: '$2',
  overflow: 'hidden',

  '&:focus-within': { borderColor: '$obolGreen' },
});

const Affix = styled('span', {
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

function TextInput({
  value,
  onChange,
  placeholder,
  prefix,
  suffix,
  mono,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  prefix?: React.ReactNode;
  suffix?: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <InputShell>
      {prefix ? <Affix side="left">{prefix}</Affix> : null}
      <NakedInput
        mono={mono}
        value={value}
        placeholder={placeholder}
        onChange={(e: ChangeEvent<HTMLInputElement>) =>
          onChange(e.target.value)
        }
      />
      {suffix ? <Affix side="right">{suffix}</Affix> : null}
    </InputShell>
  );
}

const ChainGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: 'repeat(3, 1fr)',
  gap: 8,
});

const ChainCard = styled('button', {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: '9px 12px',
  border: '1px solid $stroke',
  background: '$bg03',
  borderRadius: '$2',
  cursor: 'pointer',
  color: '$body',
  fontFamily: 'inherit',
  fontSize: '$2',
  textAlign: 'left',
  fontWeight: '$medium',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
        color: '$obolGreen',
        fontWeight: '$semibold',
      },
    },
    unavailable: {
      true: {
        cursor: 'not-allowed',
        opacity: 0.48,
      },
    },
  },
});

function ChainPicker({
  value,
  onChange,
  disabledChains = [],
  disabledReason,
}: {
  value: ChainKey;
  onChange: (k: ChainKey) => void;
  disabledChains?: readonly ChainKey[];
  disabledReason?: string;
}) {
  return (
    <ChainGrid>
      {CHAIN_OPTIONS.map(opt => {
        const disabled = disabledChains.includes(opt.k);
        return (
          <ChainCard
            key={opt.k}
            type="button"
            active={opt.k === value}
            unavailable={disabled}
            disabled={disabled}
            title={disabled ? disabledReason : undefined}
            onClick={() => {
              if (!disabled) onChange(opt.k);
            }}
          >
            <ChainIcon chain={opt.icon} size={16} />
            <span style={{ flex: 1 }}>{opt.label}</span>
          </ChainCard>
        );
      })}
    </ChainGrid>
  );
}

const VisibilityGrid = styled('div', {
  display: 'grid',
  gridTemplateColumns: '1fr 1fr',
  gap: 8,
});

const RadioCardBtn = styled('button', {
  display: 'flex',
  flexDirection: 'column',
  gap: 5,
  padding: '12px 14px',
  border: '1px solid $stroke',
  background: '$bg03',
  borderRadius: '$2',
  cursor: 'pointer',
  textAlign: 'left',
  fontFamily: 'inherit',
  minHeight: 88,
  position: 'relative',

  variants: {
    active: {
      true: {
        background: '$obolMidGreen',
        borderColor: '$obolGreen',
      },
    },
  },
});

const RadioRow = styled('div', {
  display: 'flex',
  alignItems: 'center',
  gap: 6,
});

const RadioCircle = styled('span', {
  width: 14,
  height: 14,
  borderRadius: '50%',
  border: '2px solid $stroke',
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,

  variants: {
    active: {
      true: {
        borderColor: '$obolGreen',
      },
    },
  },
});

const RadioInner = styled('span', {
  width: 6,
  height: 6,
  borderRadius: '50%',
  background: '$obolGreen',
});

const RadioTitle = styled('span', {
  fontSize: '$3',
  fontWeight: '$semibold',
  color: '$light',

  variants: {
    active: {
      true: { color: '$obolGreen' },
    },
  },
});

const RadioBadge = styled('span', {
  fontSize: '$1',
  padding: '2px 6px',
  borderRadius: '$pill',
  background: '$obolMidGreen',
  color: '$obolGreen',
  fontWeight: '$semibold',
  lineHeight: 1,
});

const RadioSub = styled('div', {
  fontSize: '$1',
  color: '$body',
  lineHeight: 1.5,
  paddingLeft: 20,
});

function VisibilityCard({
  active,
  title,
  badge,
  sub,
  onClick,
}: {
  active: boolean;
  title: string;
  badge?: string;
  sub: string;
  onClick: () => void;
}) {
  return (
    <RadioCardBtn type="button" active={active} onClick={onClick}>
      <RadioRow>
        <RadioCircle active={active}>
          {active ? <RadioInner /> : null}
        </RadioCircle>
        <RadioTitle active={active}>{title}</RadioTitle>
        <span style={{ flex: 1 }} />
        {badge ? <RadioBadge>{badge}</RadioBadge> : null}
      </RadioRow>
      <RadioSub>{sub}</RadioSub>
    </RadioCardBtn>
  );
}

// ── Footer buttons ──────────────────────────────────────────────────────

const CliCopyGroup = styled('div', {
  background: 'transparent',
  border: '1px solid transparent',
  color: '$body',
  padding: '6px 10px',
  borderRadius: '$pill',
  fontSize: '$2',
  cursor: 'default',
  display: 'inline-flex',
  alignItems: 'center',
  gap: '$xxxs',
  '&:hover': { background: '$bg03', color: '$light' },
});

const SecondaryBtn = styled('button', {
  background: '$bg03',
  border: '1px solid $stroke',
  color: '$light',
  padding: '6px 14px',
  borderRadius: '$pill',
  fontSize: '$2',
  cursor: 'pointer',
  '&:hover': { background: '$bg04' },
});

const PrimaryBtn = styled('button', {
  background: '$obolGreen',
  color: '$obolDarkDark',
  border: 0,
  padding: '6px 14px',
  borderRadius: '$pill',
  fontSize: '$2',
  fontWeight: '$semibold',
  cursor: 'pointer',
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,

  '&:hover': { filter: 'brightness(1.05)' },
  '&:disabled': { cursor: 'not-allowed', opacity: 0.55 },
});

const Banner = styled('div', {
  padding: '$xs $sm',
  borderRadius: '$2',
  fontSize: '$2',
  lineHeight: 1.4,

  variants: {
    tone: {
      info: {
        background: '$bg03',
        color: '$body',
        border: '1px solid $stroke',
      },
      error: {
        background: 'rgba(221, 96, 60, 0.10)',
        color: '$light',
        border: '1px solid rgba(221, 96, 60, 0.40)',
      },
      success: {
        background: '$obolMidGreen',
        color: '$obolGreen',
        border: '1px solid $obolGreen',
      },
    },
  },
});

// ── Main dialog ─────────────────────────────────────────────────────────

export function NewListingDialog({ open, onClose }: NewListingDialogProps) {
  const operator = useOperatorWallets();

  // Payments settle to the stack master agent wallet, not the browser-connected wallet.
  const sellerWallet = useMemo(() => {
    const master = operator.agents.find(
      a => a.runtime === 'hermes' && a.instanceId === DEFAULT_AGENT_ID
    );
    return master?.addresses[0] ?? operator.agents[0]?.addresses[0];
  }, [operator.agents]);

  const [type, setType] = useState<ServiceType>('route');
  const [name, setName] = useState('my-service');
  const [upstream, setUpstream] = useState('ollama:11434');
  const [model, setModel] = useState('qwen3.5:9b');
  const [priceAmount, setPriceAmount] = useState('0.001');
  const [priceToken, setPriceToken] = useState<PaymentToken>('USDC');
  const [priceUnit, setPriceUnit] = useState<PriceUnit>('req');
  const [chain, setChain] = useState<ChainKey>('base');
  const [visibility, setVisibility] = useState<Visibility>('erc8004');

  const [submitting, setSubmitting] = useState(false);
  const [submitState, setSubmitState] = useState<'idle' | 'success' | 'error'>(
    'idle'
  );
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [cliCopied, setCliCopied] = useState(false);

  const paymentAsset = useMemo(
    () => resolveSellAsset(chain, priceToken, CLIENT_SELL_ASSET_ENV),
    [chain, priceToken]
  );
  const paymentError = paymentAsset.error ?? null;
  const disabledChains = useMemo(
    () =>
      CHAIN_OPTIONS.filter(
        opt =>
          !isSellTokenSupportedOnChain(opt.k, priceToken, CLIENT_SELL_ASSET_ENV)
      ).map(opt => opt.k),
    [priceToken]
  );

  // Reset transient submit state each time the dialog opens.
  useEffect(() => {
    if (open) {
      setSubmitting(false);
      setSubmitState('idle');
      setErrorMessage(null);
      setCliCopied(false);
    }
  }, [open]);

  // Escape closes the dialog (only when open + not submitting).
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !submitting) onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [open, submitting, onClose]);

  const cli = useMemo(() => {
    const payTo = sellerWallet ?? '<agent wallet>';
    if (type === 'inference') {
      return [
        `obol sell inference ${name.trim() || '<name>'} \\`,
        `  --model ${model.trim() || '<model>'} \\`,
        `  --per-request ${priceAmount} \\`,
        `  --token ${priceToken} \\`,
        `  --chain ${chain} \\`,
        `  --pay-to ${payTo}`,
      ].join('\n');
    }
    return [
      `obol sell http ${name.trim() || '<name>'} \\`,
      `  --upstream ${upstream.split(':')[0]} \\`,
      `  --port ${upstream.split(':')[1] ?? '8080'} \\`,
      `  --per-request ${priceAmount} \\`,
      `  --token ${priceToken} \\`,
      `  --chain ${chain} \\`,
      `  --pay-to ${payTo}`,
    ].join('\n');
  }, [
    type,
    name,
    model,
    upstream,
    priceAmount,
    priceToken,
    chain,
    sellerWallet,
  ]);

  if (!open) return null;

  // Inference needs a host gateway — only the CLI can start that today.
  const canSubmitHttp =
    type === 'route' &&
    Boolean(sellerWallet) &&
    !submitting &&
    name.trim().length > 0 &&
    !paymentError;

  const canCopyInferenceCli =
    type === 'inference' &&
    Boolean(sellerWallet) &&
    name.trim().length > 0 &&
    model.trim().length > 0 &&
    !paymentError;

  const handleTokenChange = (token: PaymentToken) => {
    const next = reconcileSellPaymentSelection(
      chain,
      token,
      CLIENT_SELL_ASSET_ENV
    );
    setPriceToken(next.token);
    setChain(next.chain);
    setSubmitState('idle');
    setErrorMessage(null);
  };

  const handleChainChange = (nextChain: ChainKey) => {
    const next = reconcileSellPaymentSelection(
      nextChain,
      priceToken,
      CLIENT_SELL_ASSET_ENV
    );
    setChain(next.chain);
    setPriceToken(next.token);
    setSubmitState('idle');
    setErrorMessage(null);
  };

  const handleSubmit = async () => {
    if (!sellerWallet) {
      setErrorMessage(
        'No agent wallet found — deploy the stack agent (obol agent init) first.'
      );
      setSubmitState('error');
      return;
    }
    if (paymentError) {
      setErrorMessage(paymentError);
      setSubmitState('error');
      return;
    }
    setSubmitting(true);
    setSubmitState('idle');
    setErrorMessage(null);

    try {
      const resp = await fetch('/api/sell/create', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          name,
          namespace: 'llm',
          type: 'http',
          wallet: sellerWallet,
          chain,
          token: priceToken,
          pricePerRequest: priceAmount,
          upstream: upstream.split(':')[0],
          port: Number(upstream.split(':')[1] ?? '8080') || 8080,
          healthPath: '/health',
        }),
      });

      if (!resp.ok) {
        const body = await resp.text();
        throw new Error(body || `HTTP ${resp.status}`);
      }

      setSubmitState('success');
    } catch (err) {
      setSubmitState('error');
      setErrorMessage(
        err instanceof Error ? err.message : 'Failed to create listing'
      );
    } finally {
      setSubmitting(false);
    }
  };

  const handleCopyInferenceCli = async () => {
    if (!canCopyInferenceCli) return;
    try {
      await navigator.clipboard.writeText(cli);
      setCliCopied(true);
      setSubmitState('idle');
      setErrorMessage(null);
    } catch {
      setErrorMessage('Could not copy — select the command below manually.');
      setSubmitState('error');
    }
  };

  return (
    <Backdrop role="presentation" onClick={onClose}>
      <Panel
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-listing-title"
        onClick={(e: ReactMouseEvent) => e.stopPropagation()}
      >
        <Header>
          <HeaderIcon>
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
              <path
                d="M8 3v10M3 8h10"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </HeaderIcon>
          <Box css={{ flex: 1, minWidth: 0 }}>
            <Eyebrow>List a new service</Eyebrow>
            <HeaderTitle id="new-listing-title">New listing</HeaderTitle>
          </Box>
          <CloseBtn type="button" aria-label="Close" onClick={onClose}>
            <svg width="18" height="18" viewBox="0 0 16 16" fill="none">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </CloseBtn>
        </Header>

        <Body>
          {!sellerWallet ? (
            <Banner tone="info">
              No agent wallet found yet. Run{' '}
              <Mono css={{ color: '$obolGreen' }}>obol agent init</Mono> so the
              master agent wallet can receive x402 payments.
            </Banner>
          ) : (
            <Banner tone="info">
              Payments settle to the master agent wallet{' '}
              <Mono css={{ color: '$light' }}>{sellerWallet}</Mono>.
            </Banner>
          )}

          <FormField
            label="Service type"
            hint={
              type === 'inference'
                ? 'Starts a host gateway on your Mac, then publishes the route. The form fills in the CLI — run it in Terminal.'
                : "Any HTTP upstream — REST, MCP, webhook. Routed through the cluster's x402 verifier."
            }
          >
            <Segmented<ServiceType>
              value={type}
              onChange={setType}
              options={[
                { k: 'inference', label: 'Inference (LLM)' },
                { k: 'route', label: 'HTTP route' },
              ]}
            />
          </FormField>

          <Box
            css={{
              display: 'grid',
              gridTemplateColumns: '1fr 1fr',
              gap: '$sm',
            }}
          >
            <FormField
              label="Listing name"
              hint="Shown to buyers — lowercase, no spaces."
            >
              <TextInput
                mono
                value={name}
                onChange={setName}
                placeholder="my-service"
              />
            </FormField>
            {type === 'inference' ? (
              <FormField
                label="Upstream model"
                hint="Ollama model id (e.g. qwen3.5:4b)."
              >
                <TextInput
                  mono
                  value={model}
                  onChange={setModel}
                  placeholder="qwen3.5:9b"
                />
              </FormField>
            ) : (
              <FormField
                label="Upstream endpoint"
                hint="Reachable from inside the cluster."
              >
                <TextInput
                  mono
                  value={upstream}
                  onChange={setUpstream}
                  placeholder="service:8080"
                  prefix="http://"
                />
              </FormField>
            )}
          </Box>

          <FormField
            label="Pricing"
            hint={
              <>
                Buyers will be charged{' '}
                <ObolAmountSwitch amount={priceAmount} symbol={priceToken} />{' '}
                per{' '}
                {priceUnit === 'mtok'
                  ? 'million tokens'
                  : priceUnit === 'req'
                    ? 'request'
                    : 'hour'}
                .
              </>
            }
          >
            <Box
              css={{
                display: 'grid',
                gridTemplateColumns: '1.2fr 110px 140px',
                gap: 8,
              }}
            >
              <TextInput
                mono
                value={priceAmount}
                onChange={setPriceAmount}
                prefix={TOKEN_PREFIX[priceToken]}
                suffix={priceToken}
              />
              <Segmented<PaymentToken>
                value={priceToken}
                onChange={handleTokenChange}
                options={[
                  { k: 'USDC', label: 'USDC' },
                  { k: 'OBOL', label: 'OBOL' },
                ]}
              />
              <Segmented<PriceUnit>
                value={priceUnit}
                onChange={setPriceUnit}
                options={[
                  { k: 'req', label: '/ req' },
                  { k: 'mtok', label: '/ Mtok' },
                  { k: 'hour', label: '/ hr' },
                ]}
              />
            </Box>
          </FormField>

          <FormField
            label="Settlement chain"
            hint="Where x402 authorisations settle. Buyer wallets must match this chain."
          >
            <ChainPicker
              value={chain}
              onChange={handleChainChange}
              disabledChains={disabledChains}
              disabledReason={`${priceToken} is not configured on this chain.`}
            />
          </FormField>

          <FormField label="Visibility">
            <VisibilityGrid>
              <VisibilityCard
                active={visibility === 'local'}
                title="Local cluster"
                sub="Only agents inside your cluster can discover and call this listing."
                onClick={() => setVisibility('local')}
              />
              <VisibilityCard
                active={visibility === 'erc8004'}
                title="Public · ERC-8004"
                badge="Recommended"
                sub="Anchored on-chain in the public registry. Anyone with a wallet can discover and pay."
                onClick={() => setVisibility('erc8004')}
              />
            </VisibilityGrid>
          </FormField>

          {type === 'inference' ? (
            <Banner tone="info">
              Inference cannot be created from the browser yet — it needs a
              process on your Mac. Copy the command below and run it in
              Terminal, then check{' '}
              <Mono css={{ color: '$obolGreen' }}>obol sell status</Mono>.
            </Banner>
          ) : null}
          {paymentError || (submitState === 'error' && errorMessage) ? (
            <Banner tone="error">{paymentError ?? errorMessage}</Banner>
          ) : null}
          {submitState === 'success' ? (
            <Banner tone="success">
              Created. Reload the marketplace to see it appear once the
              controller reconciles.
            </Banner>
          ) : null}
          {cliCopied ? (
            <Banner tone="success">
              Copied. Run it in Terminal on this Mac, then reload the
              marketplace once{' '}
              <Mono css={{ color: '$obolGreen' }}>obol sell status</Mono> shows
              Ready.
            </Banner>
          ) : null}
        </Body>

        <Footer>
          <CliCopyGroup title={cli}>
            <CopyButton text={cli} title="Copy CLI equivalent" />
            <Text css={{ fontSize: '$2' }}>Copy CLI equivalent</Text>
          </CliCopyGroup>
          <Box css={{ flex: 1 }} />
          <SecondaryBtn type="button" onClick={onClose}>
            Cancel
          </SecondaryBtn>
          <PrimaryBtn
            type="button"
            disabled={
              type === 'inference' ? !canCopyInferenceCli : !canSubmitHttp
            }
            onClick={
              type === 'inference' ? handleCopyInferenceCli : handleSubmit
            }
          >
            {type === 'inference'
              ? 'Copy CLI command'
              : submitting
                ? 'Creating…'
                : 'Create listing'}
            {!submitting ? (
              <svg width="13" height="13" viewBox="0 0 16 16" fill="none">
                <path
                  d="M3 8h10M9 4l4 4-4 4"
                  stroke="currentColor"
                  strokeWidth="1.6"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
            ) : null}
          </PrimaryBtn>
        </Footer>
      </Panel>
    </Backdrop>
  );
}

// Mark Flex import as used — Flex is exported by obol-ui and kept here in case
// a future caller wants to compose against the same primitives the dialog uses.
export { Flex as _DialogFlex };
