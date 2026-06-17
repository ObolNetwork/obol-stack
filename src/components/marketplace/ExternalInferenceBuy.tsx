'use client';

import { useMemo, useState, type ChangeEvent } from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import { CopyButton } from '@/components/atoms/CopyButton';
import { obolBuyInferenceCommand } from '@/lib/marketplace/buy-command';

const Panel = styled(Box, {
  borderRadius: '$2',
  border: '1px solid rgba(47, 228, 171, 0.25)',
  background: 'rgba(47, 228, 171, 0.06)',
  padding: '$sm $md',
  display: 'flex',
  flexDirection: 'column',
  gap: '$sm',
});

const Title = styled(Text, {
  fontSize: '$2',
  fontWeight: '$semibold',
  color: '$light',
});

const Body = styled(Text, {
  fontSize: '$2',
  color: '$body',
  lineHeight: 1.5,
});

const FieldLabel = styled(Text, {
  fontSize: '$1',
  color: '$muted',
  textTransform: 'uppercase',
  letterSpacing: '0.04em',
});

const FieldInput = styled('input', {
  width: '100%',
  height: 36,
  padding: '0 $sm',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
  color: '$light',
  fontSize: '$2',
  fontFamily: 'monospace',

  '&::placeholder': { color: '$body' },
  '&:focus': { outline: 'none', borderColor: '$obolGreen' },
});

const CommandRow = styled(Flex, {
  alignItems: 'flex-start',
  gap: '$xxs',
  padding: '$xs',
  borderRadius: '$2',
  border: '1px solid $stroke',
  background: '$bg02',
});

const CommandText = styled(Text, {
  color: '$light',
  fontFamily: 'monospace',
  fontSize: '$1',
  lineHeight: 1.5,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
  flex: 1,
});

const DEFAULT_SELLER = 'https://inference.v1337.org/services/aeon7';
const DEFAULT_MODEL = '';
const DEFAULT_ALIAS = 'aeon7';

/** Host-side `obol buy inference` for sellers outside this cluster's catalog. */
export function ExternalInferenceBuy() {
  const [sellerUrl, setSellerUrl] = useState(DEFAULT_SELLER);
  const [model, setModel] = useState(DEFAULT_MODEL);
  const [alias, setAlias] = useState(DEFAULT_ALIAS);

  const command = useMemo(
    () =>
      obolBuyInferenceCommand(
        {
          endpoint: sellerUrl.trim() || DEFAULT_SELLER,
          modelOrRuntime: model.trim() || DEFAULT_MODEL,
          remoteModel: model.trim() || DEFAULT_MODEL,
        },
        10,
        { yes: true, name: alias.trim() || undefined }
      ),
    [sellerUrl, model, alias]
  );

  return (
    <Panel>
      <Title>Buy inference from an external seller</Title>
      <Body>
        External stacks (e.g. <code>inference.v1337.org</code>) are not in
        ERC-8004 browse or this cluster&apos;s catalog. Run on your Mac — it
        pre-signs x402 auths and mounts <code>paid/&lt;model&gt;</code> in
        LiteLLM.
      </Body>

      <Box css={{ display: 'flex', flexDirection: 'column', gap: '$xxs' }}>
        <FieldLabel>Seller URL (402 endpoint)</FieldLabel>
        <FieldInput
          value={sellerUrl}
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            setSellerUrl(e.target.value)
          }
          placeholder={DEFAULT_SELLER}
        />
      </Box>

      <Flex css={{ gap: '$sm', flexWrap: 'wrap' }}>
        <Box
          css={{
            flex: 1,
            minWidth: 140,
            display: 'flex',
            flexDirection: 'column',
            gap: '$xxs',
          }}
        >
          <FieldLabel>Remote model id (optional)</FieldLabel>
          <FieldInput
            value={model}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              setModel(e.target.value)
            }
            placeholder="auto (from catalog)"
          />
        </Box>
        <Box
          css={{
            flex: 1,
            minWidth: 140,
            display: 'flex',
            flexDirection: 'column',
            gap: '$xxs',
          }}
        >
          <FieldLabel>Local alias (optional)</FieldLabel>
          <FieldInput
            value={alias}
            onChange={(e: ChangeEvent<HTMLInputElement>) =>
              setAlias(e.target.value)
            }
            placeholder={DEFAULT_ALIAS}
          />
        </Box>
      </Flex>

      <CommandRow>
        <CommandText>{command}</CommandText>
        <CopyButton text={command} title="Copy obol buy inference" />
      </CommandRow>
    </Panel>
  );
}
