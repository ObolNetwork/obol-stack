import { useCallback, useEffect, useMemo, useState } from 'react';
import { useForm } from 'react-hook-form';
import { ChainName } from '@/types/dashboard';
import {
  CLIENT_SELL_ASSET_ENV,
  isSellTokenSupportedOnChain,
  reconcileSellPaymentSelection,
  resolveSellAsset,
  type SellPaymentToken,
} from '@/lib/services/sell-assets';
import { agentPostCreateSteps } from '@/lib/sell/flow-steps';
import type { SellFlowStep } from '@/lib/sell/flow-steps';

export interface SellAgentForm {
  wallet: string;
  chain: ChainName;
  token: SellPaymentToken;
  price: string;
}

export type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const SELL_CHAINS: ChainName[] = ['base-sepolia', 'base', 'mainnet'];

export function useSellAgentModal(
  agentName: string,
  agentNamespace: string,
  defaultWallet?: string,
  onListed?: () => void
) {
  const [open, setOpen] = useState(false);
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState('');
  const [followUpSteps, setFollowUpSteps] = useState<SellFlowStep[]>([]);

  const {
    register,
    handleSubmit,
    watch,
    reset,
    setValue,
    formState: { isValid: formIsValid },
  } = useForm<SellAgentForm>({
    defaultValues: {
      wallet: defaultWallet ?? '',
      chain: 'base',
      token: 'USDC',
      price: '0.001',
    },
    mode: 'onChange',
  });

  const chainValue = watch('chain');
  const tokenValue = watch('token');
  const priceValue = watch('price');
  const walletValue = watch('wallet');

  const paymentError = useMemo(
    () => resolveSellAsset(chainValue, tokenValue, CLIENT_SELL_ASSET_ENV).error,
    [chainValue, tokenValue]
  );

  const disabledChainValues = useMemo(
    () =>
      SELL_CHAINS.filter(
        chain =>
          !isSellTokenSupportedOnChain(chain, tokenValue, CLIENT_SELL_ASSET_ENV)
      ),
    [tokenValue]
  );

  useEffect(() => {
    const next = reconcileSellPaymentSelection(
      chainValue,
      tokenValue,
      CLIENT_SELL_ASSET_ENV
    );
    if (next.chain !== chainValue) {
      setValue('chain', next.chain, { shouldValidate: true });
    }
  }, [chainValue, setValue, tokenValue]);

  const selectPaymentToken = useCallback(
    (token: SellPaymentToken) => {
      const next = reconcileSellPaymentSelection(
        chainValue,
        token,
        CLIENT_SELL_ASSET_ENV
      );
      setValue('token', next.token, { shouldValidate: true });
      setValue('chain', next.chain, { shouldValidate: true });
      setSubmitState('idle');
      setErrorMsg('');
    },
    [chainValue, setValue]
  );

  const selectPaymentChain = useCallback(
    (chain: ChainName | undefined) => {
      if (!chain) return;
      const next = reconcileSellPaymentSelection(
        chain,
        tokenValue,
        CLIENT_SELL_ASSET_ENV
      );
      setValue('token', next.token, { shouldValidate: true });
      setValue('chain', next.chain, { shouldValidate: true });
      setSubmitState('idle');
      setErrorMsg('');
    },
    [setValue, tokenValue]
  );

  const onSubmit = useCallback(
    async (data: SellAgentForm) => {
      setSubmitState('submitting');
      setErrorMsg('');

      try {
        const paymentAsset = resolveSellAsset(
          data.chain,
          data.token,
          CLIENT_SELL_ASSET_ENV
        );
        if (paymentAsset.error) throw new Error(paymentAsset.error);

        const res = await fetch('/api/sell/create', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            name: agentName,
            namespace: agentNamespace,
            type: 'agent',
            wallet: data.wallet,
            chain: data.chain,
            token: data.token,
            pricePerRequest: data.price,
            agentRef: { name: agentName, namespace: agentNamespace },
          }),
        });

        if (!res.ok) {
          const json = await res.json();
          throw new Error(json.error || `HTTP ${res.status}`);
        }

        setFollowUpSteps(
          agentPostCreateSteps(
            {
              agentName,
              name: agentName,
              payTo: data.wallet,
              chain: data.chain,
              token: data.token,
              pricePerRequest: data.price,
            },
            agentNamespace
          )
        );
        setSubmitState('success');
        onListed?.();
      } catch (err: unknown) {
        setErrorMsg(
          err instanceof Error ? err.message : 'Failed to create ServiceOffer'
        );
        setSubmitState('error');
      }
    },
    [agentName, agentNamespace, onListed]
  );

  const handleClose = useCallback(() => {
    setOpen(false);
    setTimeout(() => {
      reset({
        wallet: defaultWallet ?? '',
        chain: 'base',
        token: 'USDC',
        price: '0.001',
      });
      setSubmitState('idle');
      setErrorMsg('');
      setFollowUpSteps([]);
    }, 200);
  }, [defaultWallet, reset]);

  return {
    open,
    setOpen,
    submitState,
    errorMsg,
    followUpSteps,
    chainValue,
    tokenValue,
    priceValue,
    walletValue,
    paymentError,
    disabledChainValues,
    isValid: formIsValid && !paymentError,
    register,
    selectPaymentToken,
    selectPaymentChain,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
