'use client';

import { useState, useCallback } from 'react';
import {
  Box,
  Flex,
  Text,
  Button,
  styled,
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogTitle,
  DialogClose,
  CloseIcon,
} from '@obolnetwork/obol-ui';

interface SellInferenceModalProps {
  trigger: React.ReactNode;
  defaultWallet?: string;
}

type FormState = {
  name: string;
  wallet: string;
  chain: string;
  price: string;
  upstream: string;
  port: string;
};

type SubmitState = 'idle' | 'submitting' | 'success' | 'error';

const ModalHeader = styled(Flex, {
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '$md $lg',
  borderBottom: '1px solid $stroke',
  background:
    'linear-gradient(180deg, rgba(47, 228, 171, 0.06) 0%, transparent 100%)',
});

const HeaderLeft = styled(Flex, {
  alignItems: 'center',
  gap: '$sm',
});

const ModalBody = styled(Box, {
  padding: '$lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
});

const FieldGroup = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '4px',
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$medium',
  letterSpacing: '0.03em',
  textTransform: 'uppercase',
});

const StyledInput = styled('input', {
  width: '100%',
  padding: '8px 12px',
  backgroundColor: '$bg01',
  border: '1px solid $stroke',
  borderRadius: '8px',
  color: '$light',
  fontSize: '14px',
  fontFamily: 'monospace',
  outline: 'none',
  transition: 'border-color 0.15s ease',
  boxSizing: 'border-box',

  '&::placeholder': {
    color: '$muted',
  },

  '&:focus': {
    borderColor: '#2FE4AB',
    boxShadow: '0 0 0 1px rgba(47, 228, 171, 0.15)',
  },
});

const InlineFields = styled(Flex, {
  gap: '$sm',
  '& > *': { flex: 1 },
});

const ModalFooter = styled(Flex, {
  padding: '$md $lg',
  borderTop: '1px solid $stroke',
  justifyContent: 'flex-end',
  gap: '$xs',
});

const CloseButton = styled('button', {
  background: 'none',
  border: 'none',
  color: '$body',
  cursor: 'pointer',
  padding: '4px',
  borderRadius: '4px',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  transition: 'color 0.15s ease',

  '&:hover': {
    color: '$light',
  },
});

const SuccessCard = styled(Flex, {
  flexDirection: 'column',
  alignItems: 'center',
  gap: '$md',
  padding: '$xl $lg',
  textAlign: 'center',
});

const PulseRing = styled(Box, {
  width: 56,
  height: 56,
  borderRadius: '50%',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'rgba(47, 228, 171, 0.1)',
  border: '2px solid rgba(47, 228, 171, 0.3)',
});

const ErrorMessage = styled(Text, {
  fontSize: '13px',
  color: '#FF6B6B',
  padding: '8px 12px',
  backgroundColor: 'rgba(255, 107, 107, 0.08)',
  borderRadius: '6px',
  border: '1px solid rgba(255, 107, 107, 0.15)',
});

const INITIAL_FORM: FormState = {
  name: '',
  wallet: '',
  chain: 'base',
  price: '0.001',
  upstream: 'litellm',
  port: '4000',
};

export function SellInferenceModal({
  trigger,
  defaultWallet,
}: SellInferenceModalProps) {
  const [open, setOpen] = useState(false);
  const [form, setForm] = useState<FormState>({
    ...INITIAL_FORM,
    wallet: defaultWallet ?? '',
  });
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState('');

  const updateField = useCallback(
    (field: keyof FormState) => (e: React.ChangeEvent<HTMLInputElement>) => {
      setForm(prev => ({ ...prev, [field]: e.target.value }));
    },
    []
  );

  const handleSubmit = useCallback(async () => {
    setSubmitState('submitting');
    setErrorMsg('');

    try {
      const res = await fetch('/api/sell/create', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: form.name,
          namespace: 'llm',
          wallet: form.wallet,
          chain: form.chain,
          pricePerRequest: form.price,
          upstream: form.upstream,
          port: parseInt(form.port, 10) || 4000,
          healthPath: '/health/readiness',
        }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || `HTTP ${res.status}`);
      }

      setSubmitState('success');
    } catch (err: any) {
      setErrorMsg(err.message || 'Failed to create ServiceOffer');
      setSubmitState('error');
    }
  }, [form]);

  const handleClose = useCallback(() => {
    setOpen(false);
    setTimeout(() => {
      setForm({ ...INITIAL_FORM, wallet: defaultWallet ?? '' });
      setSubmitState('idle');
      setErrorMsg('');
    }, 200);
  }, [defaultWallet]);

  const isValid =
    form.name.length > 0 &&
    form.wallet.startsWith('0x') &&
    form.wallet.length === 42 &&
    parseFloat(form.price) > 0;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent>
        <ModalHeader>
          <HeaderLeft>
            <DialogTitle asChild>
              <Text
                css={{
                  fontSize: '$5',
                  fontWeight: '$semibold',
                  color: '$light',
                }}
              >
                Sell Inference
              </Text>
            </DialogTitle>
          </HeaderLeft>
          <DialogClose asChild>
            <CloseButton onClick={handleClose}>
              <CloseIcon />
            </CloseButton>
          </DialogClose>
        </ModalHeader>

        {submitState === 'success' ? (
          <SuccessCard>
            <PulseRing>
              <Text css={{ color: '#2FE4AB', fontSize: '24px' }}>&#10003;</Text>
            </PulseRing>
            <Box>
              <Text
                css={{
                  fontSize: '$5',
                  fontWeight: '$semibold',
                  color: '$light',
                  marginBottom: '$xxs',
                }}
              >
                ServiceOffer Created
              </Text>
              <Text css={{ fontSize: '$2', color: '$body' }}>
                <Text
                  as="span"
                  css={{ color: '#2FE4AB', fontFamily: 'monospace' }}
                >
                  {form.name}
                </Text>{' '}
                is being reconciled by the agent.
              </Text>
              <Text
                css={{
                  fontSize: '$1',
                  color: '$muted',
                  marginTop: '$xs',
                  fontFamily: 'monospace',
                }}
              >
                obol sell status {form.name} -n llm
              </Text>
            </Box>
            <Button
              color="primary"
              onClick={handleClose}
              css={{ marginTop: '$sm' }}
            >
              Done
            </Button>
          </SuccessCard>
        ) : (
          <>
            <ModalBody>
              <Text css={{ fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
                Expose a service behind an x402 payment gate. Buyers pay
                per-request in USDC.
              </Text>

              <FieldGroup>
                <FieldLabel>Service Name</FieldLabel>
                <StyledInput
                  placeholder="my-inference"
                  value={form.name}
                  onChange={updateField('name')}
                  autoFocus
                />
              </FieldGroup>

              <FieldGroup>
                <FieldLabel>USDC Wallet Address</FieldLabel>
                <StyledInput
                  placeholder="0x..."
                  value={form.wallet}
                  onChange={updateField('wallet')}
                />
              </FieldGroup>

              <InlineFields>
                <FieldGroup>
                  <FieldLabel>Chain</FieldLabel>
                  <StyledInput
                    value={form.chain}
                    onChange={updateField('chain')}
                  />
                </FieldGroup>
                <FieldGroup>
                  <FieldLabel>Price / Request (USDC)</FieldLabel>
                  <StyledInput
                    type="number"
                    step="0.0001"
                    min="0"
                    value={form.price}
                    onChange={updateField('price')}
                  />
                </FieldGroup>
              </InlineFields>

              <InlineFields>
                <FieldGroup>
                  <FieldLabel>Upstream Service</FieldLabel>
                  <StyledInput
                    value={form.upstream}
                    onChange={updateField('upstream')}
                  />
                </FieldGroup>
                <FieldGroup>
                  <FieldLabel>Port</FieldLabel>
                  <StyledInput
                    type="number"
                    value={form.port}
                    onChange={updateField('port')}
                  />
                </FieldGroup>
              </InlineFields>

              {submitState === 'error' && (
                <ErrorMessage>{errorMsg}</ErrorMessage>
              )}
            </ModalBody>

            <ModalFooter>
              <Button
                color="secondary"
                onClick={handleClose}
                css={{ fontSize: '$3', height: '$sm' }}
              >
                Cancel
              </Button>
              <Button
                color="primary"
                disabled={!isValid || submitState === 'submitting'}
                onClick={handleSubmit}
                css={{ fontSize: '$3', height: '$sm', minWidth: 120 }}
              >
                {submitState === 'submitting' ? 'Creating...' : 'Create Offer'}
              </Button>
            </ModalFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
