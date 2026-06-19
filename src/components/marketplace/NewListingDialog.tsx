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
import { SellFlowSteps } from '@/components/molecules/SellFlowSteps';
import { useOperatorWallets } from '@/hooks/useOperatorWallets';
import { DEFAULT_AGENT_ID } from '@/lib/services/agent-runtime';
import {
  buildSellHttpCli,
  buildSellInferenceCli,
} from '@/lib/sell/cli-commands';
import { resolveUpstreamHealthPath } from '@/lib/sell/upstream-health';
import {
  httpPostCreateSteps,
  inferencePostCreateSteps,
  type SellFlowStep,
} from '@/lib/sell/flow-steps';
import type { ChainName } from '@/types/dashboard';

export interface NewListingDialogProps {
  open: boolean;
  onClose: () => void;
}

type ServiceType = 'inference' | 'route';
type PaymentToken = SellPaymentToken;
type PriceUnit = 'req' | 'mtok' | 'hour';
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
  list,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  prefix?: React.ReactNode;
  suffix?: React.ReactNode;
  mono?: boolean;
  list?: string;
}) {
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

  // Default payout: stack remote-signer wallet (lives in hermes-obol-agent). Any --pay-to works in CLI.
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
  const [payoutWallet, setPayoutWallet] = useState('');

  const defaultPayoutWallet = sellerWallet ?? '';

  const [submitting, setSubmitting] = useState(false);
  const [submitState, setSubmitState] = useState<'idle' | 'success' | 'error'>(
    'idle'
  );
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [followUpSteps, setFollowUpSteps] = useState<SellFlowStep[]>([]);

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
      setFollowUpSteps([]);
      setPayoutWallet(defaultPayoutWallet);
    }
  }, [open, defaultPayoutWallet]);

  // Escape closes the dialog (only when open + not submitting).
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !submitting) onClose();
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [open, submitting, onClose]);

  const payTo = payoutWallet.trim() || defaultPayoutWallet || '<payout wallet>';
  const cli = useMemo(() => {
    const base = {
      name: name.trim() || '<name>',
      payTo,
      chain,
      token: priceToken,
      pricePerRequest: priceAmount,
    };
    if (type === 'inference') {
      return buildSellInferenceCli({
        ...base,
        model: model.trim() || '<model>',
      });
    }
    const upstreamService = upstream.split(':')[0];
    return buildSellHttpCli({
      ...base,
      upstream: upstreamService,
      port: upstream.split(':')[1] ?? '8080',
      namespace: 'llm',
      healthPath: resolveUpstreamHealthPath(upstreamService),
    });
  }, [type, name, model, upstream, priceAmount, priceToken, chain, payTo]);

  if (!open) return null;

  const canSubmit =
    Boolean(payoutWallet.trim() || defaultPayoutWallet) &&
    /^0x[a-fA-F0-9]{40}$/.test(payoutWallet.trim() || defaultPayoutWallet) &&
    !submitting &&
    name.trim().length > 0 &&
    !paymentError &&
    (type !== 'inference' || model.trim().length > 0);

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
    const wallet = (payoutWallet.trim() || defaultPayoutWallet).trim();
    if (!wallet) {
      setErrorMessage('Payout wallet (pay-to) is required.');
      setSubmitState('error');
      return;
    }
    if (!/^0x[a-fA-F0-9]{40}$/.test(wallet)) {
      setErrorMessage('Payout wallet must be a valid 0x address.');
      setSubmitState('error');
      return;
    }
    if (type === 'inference' && !model.trim()) {
      setErrorMessage('Model name is required for inference listings.');
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
    setFollowUpSteps([]);

    const isInference = type === 'inference';
    const sellBase = {
      name: name.trim(),
      payTo: wallet,
      chain,
      token: priceToken,
      pricePerRequest: priceAmount,
    };

    try {
      const resp = await fetch('/api/sell/create', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          name: name.trim(),
          namespace: 'llm',
          type: isInference ? 'inference' : 'http',
          wallet,
          chain,
          token: priceToken,
          pricePerRequest: priceAmount,
          model: isInference ? model.trim() : undefined,
          upstream: isInference ? undefined : upstream.split(':')[0],
          port: isInference
            ? undefined
            : Number(upstream.split(':')[1] ?? '8080') || 8080,
          healthPath: isInference
            ? '/health'
            : resolveUpstreamHealthPath(upstream.split(':')[0]),
        }),
      });

      if (!resp.ok) {
        const body = await resp.text();
        throw new Error(body || `HTTP ${resp.status}`);
      }

      setFollowUpSteps(
        isInference
          ? inferencePostCreateSteps(
              { ...sellBase, model: model.trim() },
              'llm'
            )
          : httpPostCreateSteps(
              {
                ...sellBase,
                upstream: upstream.split(':')[0],
                port: Number(upstream.split(':')[1] ?? '8080') || 8080,
              },
              'llm'
            )
      );
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
          {!defaultPayoutWallet ? (
            <Banner tone="info">
              No default payout wallet found. Run{' '}
              <Mono css={{ color: '$obolGreen' }}>obol agent init</Mono> or
              enter any <Mono css={{ color: '$obolGreen' }}>pay-to</Mono>{' '}
              address below.
            </Banner>
          ) : null}

          <FormField
            label="Payout wallet (pay-to)"
            hint="Where buyer payments settle. Defaults to stack remote-signer; override for a different operator wallet."
          >
            <TextInput
              value={payoutWallet}
              onChange={v => {
                setPayoutWallet(v);
                setSubmitState('idle');
                setErrorMessage(null);
              }}
              placeholder="0x..."
              mono
            />
          </FormField>

          {type === 'inference' ? (
            <Banner tone="info">
              Inference is served from your Mac (Ollama + gateway) — unrelated
              to which model Hermes chats with. After create, run the CLI step
              shown.
            </Banner>
          ) : null}

          <FormField
            label="Service type"
            hint={
              type === 'inference'
                ? 'Creates the listing, then run the gateway command on your Mac (shown after create).'
                : "Any HTTP upstream — REST, MCP, webhook. Routed through the cluster's x402 verifier."
            }
          >
            <Segmented<ServiceType>
              value={type}
              onChange={t => {
                setType(t);
                if (t === 'inference') setModel('qwen3.5:9b');
              }}
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
                hint="Ollama model ID to sell — must be pulled on your Mac first with ollama pull <model>. Separate from which model your agents use."
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

          <Banner tone="info">
            HTTP and inference listings join the cluster marketplace catalog
            when ready. On-chain ERC-8004 registration is available for agent
            listings — sell a sub-agent from the dashboard, then run{' '}
            <Mono css={{ color: '$obolGreen' }}>obol sell register</Mono>.
          </Banner>

          {type === 'inference' ? (
            <Banner tone="info">
              Creates the listing in the cluster. You still need to run{' '}
              <Mono css={{ color: '$obolGreen' }}>obol sell inference</Mono> on
              your Mac — the numbered steps appear after create.
            </Banner>
          ) : null}
          {paymentError || (submitState === 'error' && errorMessage) ? (
            <Banner tone="error">{paymentError ?? errorMessage}</Banner>
          ) : null}
          {submitState === 'success' && followUpSteps.length > 0 ? (
            <SellFlowSteps title="Next steps" steps={followUpSteps} />
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
            disabled={!canSubmit}
            onClick={handleSubmit}
          >
            {submitting ? 'Creating…' : 'Create listing'}
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
