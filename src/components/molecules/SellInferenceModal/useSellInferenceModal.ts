import { useState, useCallback } from 'react';
import { useForm } from 'react-hook-form';

export type FormState = {
  name: string;
  wallet: string;
  chain: string;
  price: string;
  upstream: string;
  port: string;
};

export type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const INITIAL_FORM: FormState = {
  name: '',
  wallet: '',
  chain: 'base-sepolia',
  price: '0.001',
  upstream: 'litellm',
  port: '4000',
};

export function useSellInferenceModal() {
  const [open, setOpen] = useState(false);
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState('');

  const {
    register,
    handleSubmit,
    watch,
    reset,
    formState: { isValid },
  } = useForm<FormState>({
    defaultValues: INITIAL_FORM,
    mode: 'onChange',
  });

  const nameValue = watch('name');

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
      reset(INITIAL_FORM);
      setSubmitState('idle');
      setErrorMsg('');
    }, 200);
  }, [reset]);

  return {
    open,
    setOpen,
    submitState,
    errorMsg,
    nameValue,
    isValid,
    register,
    handleSubmit,
    onSubmit,
    handleClose,
  };
}
