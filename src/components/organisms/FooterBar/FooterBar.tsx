import React from 'react';
import { Box, Flex, Text, styled } from '@obolnetwork/obol-ui';
import {
  FileTextIcon,
  GitHubLogoIcon,
  DiscordLogoIcon,
} from '@radix-ui/react-icons';
import { isFeatureEnabled } from '@/lib/config/featureFlags';
import { config } from '../../../lib/config/environment';

export interface FooterBarProps {
  className?: string;
}

const StyledFooter = styled(Box, {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
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

export function FooterBar({ className }: FooterBarProps) {
  const showVersionInfo = isFeatureEnabled('showTagVersionAndShaCommit');

  // TODO: Implement diagnostics export
  // const handleExportDiagnostics = () => {
  //
  //   console.log('Export diagnostics clicked');
  // };

  // TODO: Implement view logs
  // const handleViewLogs = () => {
  //
  //   console.log('View logs clicked');
  // };

  return (
    <StyledFooter className={className} as="footer">
      <Flex css={{ alignItems: 'center', gap: '$sm', width: 'max-content' }}>
        {showVersionInfo && (
          <Flex
            css={{
              alignContent: 'center',
              gap: '$xxs',
            }}
          >
            <Text
              css={{
                display: 'grid',
                placeItems: 'center',
                fontSize: '$3',
                whiteSpace: 'nowrap',
              }}
            >
              Version info:
            </Text>
            <Flex as="span">
              <StyledLink
                data-testid="version-tag-info"
                target="_blank"
                rel="noopener noreferrer"
                aria-label="Version info"
                href={`https://github.com/ObolNetwork/obol-stack-front-end/releases/tag/${config.tagVersion}`}
              >
                {`${config.tagVersion}   -`}
              </StyledLink>
              <StyledLink
                data-testid="version-commit-info"
                target="_blank"
                rel="noopener noreferrer"
                aria-label="Version info"
                href={`https://github.com/ObolNetwork/obol-stack-front-end/commit/${config.commitSha}`}
              >
                {config.commitSha}
              </StyledLink>
            </Flex>
          </Flex>
        )}
        {/* <StyledButton onClick={handleExportDiagnostics}>
          <IconWrapper>
            <DownloadIcon />
          </IconWrapper>
          Export Diagnostics
        </StyledButton>
        <StyledButton onClick={handleViewLogs}>
          <IconWrapper>
            <FileTextIcon />
          </IconWrapper>
          View Logs
        </StyledButton> */}
      </Flex>

      <Flex css={{ alignItems: 'center', gap: '$sm', width: 'fit-content' }}>
        <StyledLink
          href="https://github.com/obolnetwork"
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
          href="https://docs.obol.org/"
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
