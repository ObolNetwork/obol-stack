'use client';

import React from 'react';
import { Box, Flex, Text, Link, styled } from '@obolnetwork/obol-ui';
import {
  FileTextIcon,
  GitHubLogoIcon,
  DiscordLogoIcon,
} from '@radix-ui/react-icons';
import { isFeatureEnabled } from '@/lib/config/featureFlags';
import { useStackVersion } from '@/hooks/useStackVersion';

export interface FooterBarProps {
  className?: string;
}

const StyledFooter = styled(Box, {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  flexWrap: 'wrap',
  gap: '$sm',
  fontSize: '$3',
  color: '$body',
});

const StyledLink = styled('a', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xxs',
  color: 'inherit',
  textDecoration: 'none',
  borderRadius: '$2',
  padding: '$xxxs',
  transition: 'color 200ms ease',

  '&:hover': {
    color: '$light',
  },

  '&:focus': {
    outline: 'none',
    boxShadow: '0 0 0 2px $obolGreen',
  },
});

const IconWrapper = styled(Box, {
  width: '$sm',
  height: '$sm',
});

function releaseHref(repo: 'obol-stack' | 'obol-stack-front-end', tag: string) {
  const normalized = tag.startsWith('v') ? tag : `v${tag}`;
  return `https://github.com/ObolNetwork/${repo}/releases/tag/${normalized}`;
}

export function FooterBar({ className }: FooterBarProps) {
  const showCommitDetail = isFeatureEnabled('showTagVersionAndShaCommit');
  const { obolVersion, frontendTag, frontendCommit } = useStackVersion();

  const obolLabel = obolVersion?.trim() || 'unknown';
  const uiLabel = frontendTag?.trim() || 'local';

  return (
    <StyledFooter className={className} as="footer">
      {/* Match dv-launchpad Version info: layout + typography */}
      <Box
        css={{
          display: 'flex',
          width: '$full',
          alignContent: 'center',
          gap: '$xxs',
          '@sm': {
            marginTop: '$xs',
          },
        }}
      >
        <Text
          variant="metadata"
          css={{
            display: 'grid',
            placeItems: 'center',
            fontSize: '$3',
            width: '6rem',
          }}
        >
          Version info:
        </Text>
        <Flex as="span" css={{ alignItems: 'center', fontSize: '$3' }}>
          <Text as="span" css={{ fontSize: '$3', color: '$body' }}>
            obol-
          </Text>
          <Link
            data-testid="obol-version-info"
            target="_blank"
            rel="noopener noreferrer"
            aria-label={`Obol stack version ${obolLabel}`}
            href={
              obolVersion
                ? releaseHref('obol-stack', obolVersion)
                : 'https://github.com/ObolNetwork/obol-stack/releases'
            }
            css={{
              fontSize: '$3',
              color: '$obolGreen',
            }}
          >
            {obolLabel}
          </Link>
          <Text as="span" css={{ fontSize: '$3', color: '$body' }}>
            , front-end-
          </Text>
          <Link
            data-testid="version-tag-info"
            target="_blank"
            rel="noopener noreferrer"
            aria-label={`Front-end tag ${uiLabel}`}
            href={releaseHref('obol-stack-front-end', uiLabel)}
            css={{
              fontSize: '$3',
              color: '$obolGreen',
            }}
          >
            {showCommitDetail ? `${uiLabel}-` : uiLabel}
          </Link>
          {showCommitDetail && (
            <Link
              data-testid="version-commit-info"
              target="_blank"
              rel="noopener noreferrer"
              aria-label="Front-end commit"
              href={`https://github.com/ObolNetwork/obol-stack-front-end/commit/${frontendCommit}`}
              css={{
                fontSize: '$3',
                color: '$obolGreen',
              }}
            >
              {frontendCommit}
            </Link>
          )}
        </Flex>
      </Box>

      <Flex css={{ alignItems: 'center', gap: '$sm', width: 'fit-content' }}>
        <StyledLink
          href="https://github.com/obolnetwork/obol-stack"
          target="_blank"
          rel="noopener noreferrer"
        >
          <IconWrapper>
            <GitHubLogoIcon />
          </IconWrapper>
          GitHub
        </StyledLink>
        <StyledLink
          href="https://discord.com/invite/n6ebKsX46w"
          target="_blank"
          rel="noopener noreferrer"
        >
          <IconWrapper>
            <DiscordLogoIcon />
          </IconWrapper>
          Discord
        </StyledLink>
        <StyledLink
          href="https://docs.obol.org/obol-stack/"
          target="_blank"
          rel="noopener noreferrer"
        >
          <IconWrapper>
            <FileTextIcon />
          </IconWrapper>
          Docs
        </StyledLink>
      </Flex>
    </StyledFooter>
  );
}
