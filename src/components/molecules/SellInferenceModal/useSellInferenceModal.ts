import { useState, useCallback, useEffect, useMemo } from 'react';
import { useForm } from 'react-hook-form';
import { ChainName } from '@/types/dashboard';
import {
  CLIENT_SELL_ASSET_ENV,
  isSellTokenSupportedOnChain,
  reconcileSellPaymentSelection,
  resolveSellAsset,
  type SellPaymentToken,
} from '@/lib/services/sell-assets';

export type FormState = {
  name: string;
  wallet: string;
  chain: ChainName;
  token: SellPaymentToken;
  price: string;
  upstream: string;
  port: string;
};

export type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const INITIAL_FORM: FormState = {
  name: '',
  wallet: '',
  chain: 'base',
  token: 'USDC',
  price: '0.001',
  upstream: 'litellm',
  port: '4000',
};

const SELL_CHAINS: ChainName[] = ['base-sepolia', 'base', 'mainnet'];

export function useSellInferenceModal(defaultWallet?: string) {
  const [open, setOpen] = useState(false);
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState('');

  const {
    register,
    handleSubmit,
    watch,
    reset,
    setValue,
    formState: { isValid: formIsValid },
  } = useForm<FormState>({
    defaultValues: { ...INITIAL_FORM, wallet: defaultWallet ?? '' },
    mode: 'onChange',
  });

  const nameValue = watch('name');
  const chainValue = watch('chain');
  const tokenValue = watch('token');
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
    const nextSelection = reconcileSellPaymentSelection(
      chainValue,
      tokenValue,
      CLIENT_SELL_ASSET_ENV
    );
    if (nextSelection.chain !== chainValue) {
      setValue('chain', nextSelection.chain, { shouldValidate: true });
    }
  }, [chainValue, setValue, tokenValue]);

  const selectPaymentToken = useCallback(
    (token: SellPaymentToken) => {
      const nextSelection = reconcileSellPaymentSelection(
        chainValue,
        token,
        CLIENT_SELL_ASSET_ENV
      );
      setValue('token', nextSelection.token, { shouldValidate: true });
      setValue('chain', nextSelection.chain, { shouldValidate: true });
      setSubmitState('idle');
      setErrorMsg('');
    },
    [chainValue, setValue]
  );

  const selectPaymentChain = useCallback(
    (chain: ChainName | undefined) => {
      if (!chain) return;
      const nextSelection = reconcileSellPaymentSelection(
        chain,
        tokenValue,
        CLIENT_SELL_ASSET_ENV
      );
      setValue('token', nextSelection.token, { shouldValidate: true });
      setValue('chain', nextSelection.chain, { shouldValidate: true });
      setSubmitState('idle');
      setErrorMsg('');
    },
    [setValue, tokenValue]
  );

  const onSubmit = useCallback(async (data: FormState) => {
    setSubmitState('submitting');
    setErrorMsg('');

    try {
      const paymentAsset = resolveSellAsset(
        data.chain,
        data.token,
        CLIENT_SELL_ASSET_ENV
      );
      if (paymentAsset.error) {
        throw new Error(paymentAsset.error);
      }

      const res = await fetch('/api/sell/create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: data.name,
          namespace: 'llm',
          type: 'inference',
          wallet: data.wallet,
          chain: data.chain,
          token: data.token,
          pricePerRequest: data.price,
          upstream: data.upstream,
          port: parseInt(data.port, 10) || 4000,
          healthPath: '/health/readiness',
        }),
      });

      if (!res.ok) {
        const json = await res.json();
        throw new Error(json.error || `HTTP ${res.status}`);
      }

      setSubmitState('success');
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to create ServiceOffer');
      setSubmitState('error');
    }
  }, []);

  const handleClose = useCallback(() => {
    setOpen(false);
    setTimeout(() => {
      reset({ ...INITIAL_FORM, wallet: defaultWallet ?? '' });
      setSubmitState('idle');
      setErrorMsg('');
    }, 200);
  }, [reset, defaultWallet]);

  return {
    open,
    setOpen,
    submitState,
    errorMsg,
    nameValue,
    chainValue,
    tokenValue,
    paymentError,
    disabledChainValues,
    isValid: formIsValid && !paymentError,
    register,
    setValue,
    selectPaymentToken,
    selectPaymentChain,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
