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
import { inferencePostCreateSteps } from '@/lib/sell/flow-steps';
import type { SellFlowStep } from '@/lib/sell/flow-steps';

export interface SellInferenceForm {
  name: string;
  model: string;
  wallet: string;
  chain: ChainName;
  token: SellPaymentToken;
  price: string;
}

export type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const SELL_CHAINS: ChainName[] = ['base-sepolia', 'base', 'mainnet'];

export function useSellInferenceModal(defaultWallet?: string) {
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
  } = useForm<SellInferenceForm>({
    defaultValues: {
      name: '',
      model: 'qwen3.5:4b',
      wallet: defaultWallet ?? '',
      chain: 'base',
      token: 'USDC',
      price: '0.001',
    },
    mode: 'onChange',
  });

  const nameValue = watch('name');
  const modelValue = watch('model');
  const chainValue = watch('chain');
  const tokenValue = watch('token');
  const priceValue = watch('price');

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

  const onSubmit = useCallback(async (data: SellInferenceForm) => {
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
          name: data.name.trim(),
          namespace: 'llm',
          type: 'inference',
          wallet: data.wallet,
          chain: data.chain,
          token: data.token,
          pricePerRequest: data.price,
          model: data.model.trim(),
        }),
      });

      if (!res.ok) {
        const json = await res.json();
        throw new Error(json.error || `HTTP ${res.status}`);
      }

      setFollowUpSteps(
        inferencePostCreateSteps(
          {
            name: data.name.trim(),
            model: data.model.trim(),
            payTo: data.wallet,
            chain: data.chain,
            token: data.token,
            pricePerRequest: data.price,
          },
          'llm'
        )
      );
      setSubmitState('success');
    } catch (err: unknown) {
      setErrorMsg(
        err instanceof Error ? err.message : 'Failed to create ServiceOffer'
      );
      setSubmitState('error');
    }
  }, []);

  const handleClose = useCallback(() => {
    setOpen(false);
    setTimeout(() => {
      reset({
        name: '',
        model: 'qwen3.5:4b',
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
    nameValue,
    modelValue,
    chainValue,
    tokenValue,
    paymentError,
    disabledChainValues,
    isValid: formIsValid && !paymentError && modelValue.trim().length > 0,
    register,
    selectPaymentToken,
    selectPaymentChain,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
