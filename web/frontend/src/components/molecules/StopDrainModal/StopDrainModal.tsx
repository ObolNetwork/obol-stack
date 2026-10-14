'use client';

import { useState, type ReactNode } from 'react';
import {
  Box,
  Flex,
  Text,
  styled,
  DialogTitle,
  ResponsiveDialog,
} from '@obolnetwork/obol-ui';
import { Button } from '@/components/atoms/Button';
import { tones } from '@/lib/ui/tones';

interface StopDrainModalProps {
  trigger: ReactNode;
  name: string;
  namespace: string;
  onDrained?: (result: { drainEndsAt: string; grace: string }) => void;
}

const ModalContent = styled(Box, {
  maxWidth: 440,
  width: '100%',
});

const ModalBody = styled(Box, {
  padding: '$lg',
  display: 'flex',
  flexDirection: 'column',
  gap: '$md',
  flex: 1,
});

const ModalFooter = styled(Flex, {
  padding: '$md $lg',
  borderTop: '1px solid $stroke',
  justifyContent: 'flex-end',
  gap: '$xs',
});

const ErrorMessage = styled(Text, {
  fontSize: '$2',
  color: tones.danger.text,
  padding: '$xxs $xs',
  backgroundColor: tones.danger.bg,
  borderRadius: '$2',
  border: `1px solid ${tones.danger.border}`,
});

const ResourceLine = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  fontFamily: 'monospace',
});

const HintBlock = styled(Text, {
  fontSize: '$1',
  color: '$body',
  lineHeight: 1.5,
  padding: '$xxs $xs',
  backgroundColor: 'rgba(255, 255, 255, 0.03)',
  border: '1px solid $stroke',
  borderRadius: '$2',
});

const Mono = styled('code', {
  fontFamily: 'monospace',
  color: '$light',
});

// Destructive primary: danger-toned confirm for taking a service off sale.
const ConfirmButton = styled(Button, {
  background: tones.danger.bg,
  borderColor: tones.danger.border,
  color: tones.danger.text,

  '&:hover:not(:disabled)': {
    background: tones.danger.bg,
    borderColor: tones.danger.text,
    color: tones.danger.text,
  },
});

type SubmitState = 'idle' | 'submitting' | 'error';

export function StopDrainModal({
  trigger,
  name,
  namespace,
  onDrained,
}: StopDrainModalProps) {
  const [open, setOpen] = useState(false);
  const [submitState, setSubmitState] = useState<SubmitState>('idle');
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const close = () => {
    setOpen(false);
    setSubmitState('idle');
    setErrorMsg(null);
  };

  const submit = async () => {
    setSubmitState('submitting');
    setErrorMsg(null);
    try {
      const response = await fetch('/api/sell/stop', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name,
          namespace,
        }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data?.error || `Drain failed (${response.status})`);
      }
      onDrained?.({
        drainEndsAt: data.drainEndsAt,
        grace: data.grace,
      });
      close();
    } catch (err) {
      setSubmitState('error');
      setErrorMsg(err instanceof Error ? err.message : 'unknown error');
    }
  };

  return (
    <ResponsiveDialog
      open={open}
      setOpen={setOpen}
      triggerElement={trigger as any}
      // The library dialog is fixed at 559x660 on desktop; shrink to content.
      css={{ '@bp2': { width: '90vw', maxWidth: 480, height: 'auto' } }}
    >
      <ModalContent>
        <Flex css={{ paddingLeft: '$xs' }}>
          <DialogTitle
            css={{
              margin: 0,
              fontSize: '$5',
              fontWeight: '$semibold',
              color: '$light',
            }}
          >
            Stop selling
          </DialogTitle>
        </Flex>

        <ModalBody>
          <Text css={{ fontSize: '$2', color: '$body', lineHeight: 1.5 }}>
            Take{' '}
            <Text
              as="span"
              css={{
                display: 'inline',
                color: '$obolGreen',
                fontFamily: 'monospace',
              }}
            >
              {name}
            </Text>{' '}
            off your storefront? New buyers stop immediately. Anyone with an
            in-flight payment gets a grace window to finish. Your agent keeps
            running — nothing is deleted.
          </Text>

          <ResourceLine>
            {namespace}/{name}
          </ResourceLine>

          <HintBlock>
            You can put it back on sale any time with{' '}
            <Mono>obol sell agent</Mono> — or remove it fully with{' '}
            <Mono>obol sell delete</Mono>.
          </HintBlock>

          {submitState === 'error' && errorMsg && (
            <ErrorMessage>{errorMsg}</ErrorMessage>
          )}
        </ModalBody>

        <ModalFooter>
          <Button
            type="button"
            variant="ghost"
            onClick={close}
            disabled={submitState === 'submitting'}
          >
            Cancel
          </Button>
          <ConfirmButton
            type="button"
            onClick={submit}
            disabled={submitState === 'submitting'}
          >
            {submitState === 'submitting' ? 'Stopping…' : 'Stop selling'}
          </ConfirmButton>
        </ModalFooter>
      </ModalContent>
    </ResponsiveDialog>
  );
}
