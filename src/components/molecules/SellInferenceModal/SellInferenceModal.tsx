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
import { Button as BaseButton } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';
import { SectionTitle } from '../../atoms/SectionTitle';
import { ChainSelector } from '../../atoms/ChainSelector';
import { SellFlowSteps } from '../SellFlowSteps';
import { inferencePreflightSteps } from '@/lib/sell/flow-steps';
import { useSellInferenceModal } from './useSellInferenceModal';
import type { SellPaymentToken } from '@/lib/services/sell-assets';

interface SellInferenceModalProps {
  trigger: React.ReactNode;
  defaultWallet?: string;
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

const TokenToggle = styled(BaseButton, {
  variants: {
    active: {
      true: {
        background: tones.success.bg,
        borderColor: tones.success.border,
        color: tones.success.text,
        '&:hover:not(:disabled)': {
          background: tones.success.bg,
          borderColor: tones.success.border,
          color: tones.success.text,
        },
      },
    },
  },
});

const ModalFooter = styled(Flex, {
  padding: '$md $lg',
  borderTop: '1px solid $stroke',
  justifyContent: 'flex-end',
  gap: '$xs',
});

const ErrorMessage = styled(Text, {
  fontSize: '$2',
  color: '$test',
  padding: '$xxs $xs',
  backgroundColor: 'rgba(255, 107, 107, 0.08)',
  borderRadius: '$2',
  border: '1px solid rgba(255, 107, 107, 0.15)',
});

export function SellInferenceModal({
  trigger,
  defaultWallet,
}: SellInferenceModalProps) {
  const {
    open,
    setOpen,
    submitState,
    errorMsg,
    followUpSteps,
    nameValue,
    chainValue,
    tokenValue,
    paymentError,
    disabledChainValues,
    isValid,
    register,
    selectPaymentToken,
    selectPaymentChain,
    handleSubmit,
    onSubmit,
    handleClose,
  } = useSellInferenceModal(defaultWallet);

  return (
    <ResponsiveDialog
      open={open}
      setOpen={setOpen}
      triggerElement={trigger as any}
    >
      <Flex css={{ paddingLeft: '$xs' }}>
        <SectionTitle>Sell inference</SectionTitle>
      </Flex>

      {submitState === 'success' ? (
        <ModalBody>
          <Text css={{ fontSize: '$2', color: '$body' }}>
            ServiceOffer{' '}
            <Text
              as="span"
              css={{ color: '$obolGreen', fontFamily: 'monospace' }}
            >
              {nameValue}
            </Text>{' '}
            saved. Finish setup on your Mac:
          </Text>
          <SellFlowSteps steps={followUpSteps} />
          <Button
            color="primary"
            onClick={handleClose}
            css={{ alignSelf: 'flex-end' }}
          >
            Done
          </Button>
        </ModalBody>
      ) : (
        <form
          onSubmit={handleSubmit(onSubmit)}
          style={{ display: 'flex', flexDirection: 'column', flex: 1 }}
        >
          <ModalBody>
            <SellFlowSteps
              title="How it works"
              steps={inferencePreflightSteps()}
            />

            <FieldGroup>
              <FieldLabel>Service name</FieldLabel>
              <TextField
                placeholder="my-inference"
                autoFocus
                {...register('name', { required: true })}
              />
            </FieldGroup>

            <FieldGroup>
              <FieldLabel>Ollama model</FieldLabel>
              <TextField
                placeholder="qwen3.5:4b"
                {...register('model', { required: true })}
              />
            </FieldGroup>

            <FieldGroup>
              <FieldLabel>Payout wallet (pay-to)</FieldLabel>
              <TextField
                placeholder="0x..."
                {...register('wallet', {
                  required: true,
                  validate: v => v.startsWith('0x') && v.length === 42,
                })}
              />
              <Text css={{ fontSize: '$1', color: '$muted', marginTop: 4 }}>
                Where buyers&apos; payments settle. Not related to which model
                Hermes chats with — inference runs on your Mac via Ollama.
              </Text>
            </FieldGroup>

            <InlineFields>
              <FieldGroup>
                <FieldLabel>Token</FieldLabel>
                <Flex css={{ gap: '$xs' }}>
                  {(['USDC', 'OBOL'] as SellPaymentToken[]).map(token => (
                    <TokenToggle
                      key={token}
                      type="button"
                      variant="secondary"
                      size="sm"
                      pill
                      active={tokenValue === token}
                      onClick={() => selectPaymentToken(token)}
                    >
                      {token}
                    </TokenToggle>
                  ))}
                </Flex>
              </FieldGroup>
              <FieldGroup>
                <FieldLabel>Chain</FieldLabel>
                <ChainSelector
                  value={chainValue}
                  onChange={selectPaymentChain}
                  disabledValues={disabledChainValues}
                  disabledReason={`${tokenValue} is not configured on this chain.`}
                />
              </FieldGroup>
            </InlineFields>

            <FieldGroup>
              <FieldLabel>Price / request ({tokenValue})</FieldLabel>
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

            {(paymentError || submitState === 'error') && (
              <ErrorMessage>{paymentError ?? errorMsg}</ErrorMessage>
            )}
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
              {submitState === 'submitting'
                ? 'Putting on sale…'
                : 'Put on sale'}
            </Button>
          </ModalFooter>
        </form>
      )}
    </ResponsiveDialog>
  );
}
