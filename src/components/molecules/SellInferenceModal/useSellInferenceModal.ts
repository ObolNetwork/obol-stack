import { useState, useCallback, useEffect } from 'react';
import { useForm } from 'react-hook-form';
import { ChainName } from '@/types/dashboard';
import type { SellPaymentToken } from '@/lib/services/sell-assets';

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
    formState: { isValid },
  } = useForm<FormState>({
    defaultValues: { ...INITIAL_FORM, wallet: defaultWallet ?? '' },
    mode: 'onChange',
  });

  const nameValue = watch('name');
  const chainValue = watch('chain');
  const tokenValue = watch('token');

  useEffect(() => {
    if (tokenValue === 'OBOL' && chainValue !== 'mainnet') {
      setValue('chain', 'mainnet', { shouldValidate: true });
    }
  }, [chainValue, setValue, tokenValue]);

  const onSubmit = useCallback(async (data: FormState) => {
    setSubmitState('submitting');
    setErrorMsg('');

    try {
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
    isValid,
    register,
    setValue,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
