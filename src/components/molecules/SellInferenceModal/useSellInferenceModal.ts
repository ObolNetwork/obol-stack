import { useState, useCallback } from 'react';
import { useForm } from 'react-hook-form';
import { ChainName } from '@/types/dashboard';

export type FormState = {
  name: string;
  wallet: string;
  chain: ChainName;
  price: string;
  upstream: string;
  port: string;
};

export type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const INITIAL_FORM: FormState = {
  name: '',
  wallet: '',
  chain: 'base',
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
          wallet: data.wallet,
          chain: data.chain,
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
    isValid,
    register,
    setValue,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
