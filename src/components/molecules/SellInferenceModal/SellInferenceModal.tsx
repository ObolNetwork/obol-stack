'use client';

import {
  Box,
  Flex,
  Text,
  Button,
  TextField,
  styled,
  ResponsiveDialog,
} from '@obolnetwork/obol-ui';
import { SectionTitle } from '../../atoms/SectionTitle';
import { useSellInferenceModal } from './useSellInferenceModal';

interface SellInferenceModalProps {
  trigger: React.ReactNode;
}

const ModalBody = styled(Box, {
  padding: '$lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  flex: 1,
});

const FieldGroup = styled(Box, {
  display: 'flex',
  flexDirection: 'column',
  gap: '$xxxs',
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  color: '$body',
  fontWeight: '$medium',
  letterSpacing: '0.03em',
  textTransform: 'uppercase',
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

const SuccessCard = styled(Flex, {
  height: '100%',
  flexDirection: 'column',
  alignItems: 'center',
  gap: '$md',
  padding: '$4xl $lg',
  textAlign: 'center',
});

const PulseRing = styled(Box, {
  width: 56,
  height: 56,
  borderRadius: '$round',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'rgba(47, 228, 171, 0.1)',
  border: '2px solid rgba(47, 228, 171, 0.3)',
});

const ErrorMessage = styled(Text, {
  fontSize: '$2',
  color: '$test',
  padding: '$xxs $xs',
  backgroundColor: 'rgba(255, 107, 107, 0.08)',
  borderRadius: '$2',
  border: '1px solid rgba(255, 107, 107, 0.15)',
});

export function SellInferenceModal({ trigger }: SellInferenceModalProps) {
  const {
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
  } = useSellInferenceModal();

  return (
    <ResponsiveDialog
      open={open}
      setOpen={setOpen}
      triggerElement={trigger as any}
    >
      <Flex css={{ paddingLeft: '$xs' }}>
        <SectionTitle>Sell Inference</SectionTitle>
      </Flex>

      {submitState === 'success' ? (
        <SuccessCard>
          <PulseRing>
            <Text css={{ color: '$obolGreen', fontSize: '$8' }}>&#10003;</Text>
          </PulseRing>
          <Box>
            <Text
              css={{
                fontSize: '$5',
                fontWeight: '$semibold',
                color: '$light',
                marginBottom: '$md',
              }}
            >
              ServiceOffer Created
            </Text>
            <Text css={{ fontSize: '$2', color: '$body', marginBottom: '$md' }}>
              <Text
                as="span"
                css={{
                  color: '$obolGreen',
                  fontFamily: 'monospace',
                  marginBottom: '$md',
                }}
              >
                {nameValue}
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
              obol sell status {nameValue} -n llm
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
        <form
          onSubmit={handleSubmit(onSubmit)}
          style={{
            display: 'flex',
            flexDirection: 'column',
            flex: 1,
            height: '100%',
          }}
        >
          <ModalBody>
            <Text css={{ fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
              Expose a service behind an x402 payment gate. Buyers pay
              per-request in USDC.
            </Text>

            <FieldGroup>
              <FieldLabel>Service Name</FieldLabel>
              <TextField
                placeholder="my-inference"
                autoFocus
                {...register('name', { required: true })}
              />
            </FieldGroup>

            <FieldGroup>
              <FieldLabel>USDC Wallet Address</FieldLabel>
              <TextField
                placeholder="0x..."
                {...register('wallet', {
                  required: true,
                  validate: v => v.startsWith('0x') && v.length === 42,
                })}
              />
            </FieldGroup>

            <InlineFields>
              <FieldGroup>
                <FieldLabel>Chain</FieldLabel>
                <TextField {...register('chain', { required: true })} />
              </FieldGroup>
              <FieldGroup>
                <FieldLabel>Price / Request (USDC)</FieldLabel>
                <TextField
                  type="number"
                  step="0.0001"
                  min="0"
                  {...register('price', {
                    required: true,
                    validate: v => parseFloat(v) > 0,
                  })}
                />
              </FieldGroup>
            </InlineFields>

            <InlineFields>
              <FieldGroup>
                <FieldLabel>Upstream Service</FieldLabel>
                <TextField {...register('upstream', { required: true })} />
              </FieldGroup>
              <FieldGroup>
                <FieldLabel>Port</FieldLabel>
                <TextField
                  type="number"
                  {...register('port', { required: true })}
                />
              </FieldGroup>
            </InlineFields>

            {submitState === 'error' && <ErrorMessage>{errorMsg}</ErrorMessage>}
          </ModalBody>

          <ModalFooter>
            <Button color="secondary" type="button" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              color="primary"
              type="submit"
              disabled={!isValid || submitState === 'submitting'}
            >
              {submitState === 'submitting' ? 'Creating...' : 'Create Offer'}
            </Button>
          </ModalFooter>
        </form>
      )}
    </ResponsiveDialog>
  );
}
