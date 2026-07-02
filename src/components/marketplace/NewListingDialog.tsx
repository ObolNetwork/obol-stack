'use client';

import type { MouseEvent as ReactMouseEvent } from 'react';
import { useEffect, useMemo, useState } from 'react';
import { Box, Text, styled } from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { CopyButton } from '@/components/atoms/CopyButton';
import { ObolAmountSwitch } from '@/components/atoms/ObolAmountSwitch';
import {
  CLIENT_SELL_ASSET_ENV,
  isSellTokenSupportedOnChain,
  reconcileSellPaymentSelection,
  resolveSellAsset,
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

import {
  Backdrop,
  Banner,
  Body,
  Eyebrow,
  Footer,
  Header,
  HeaderIcon,
  HeaderTitle,
  Panel,
} from './new-listing/DialogShell';
import { FormField, TextInput } from './new-listing/FormField';
import { Segmented } from './new-listing/Segmented';
import {
  CHAIN_OPTIONS,
  ChainPicker,
  type ChainKey,
} from './new-listing/ChainPicker';

export interface NewListingDialogProps {
  open: boolean;
  onClose: () => void;
}

type ServiceType = 'inference' | 'route';
type PaymentToken = SellPaymentToken;
type PriceUnit = 'req' | 'mtok' | 'hour';

const Mono = styled('span', { fontFamily: 'monospace' });

const TOKEN_PREFIX: Record<PaymentToken, string> = {
  USDC: '$',
  OBOL: 'Ⓞ',
};

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
          <Button
            type="button"
            variant="bare"
            size="sm"
            iconOnly
            aria-label="Close"
            onClick={onClose}
          >
            <svg width="18" height="18" viewBox="0 0 16 16" fill="none">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="1.6"
                strokeLinecap="round"
              />
            </svg>
          </Button>
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
          <Button type="button" variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            variant="primary"
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
          </Button>
        </Footer>
      </Panel>
    </Backdrop>
  );
}
