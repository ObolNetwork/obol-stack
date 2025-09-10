import React from 'react';
import { Box, Flex, styled } from '@obolnetwork/obol-ui';
import {
  DownloadIcon,
  FileTextIcon,
  GitHubLogoIcon,
  ChatBubbleIcon,
} from '@radix-ui/react-icons';

export interface FooterBarProps {
  className?: string;
}

const StyledFooter = styled(Box, {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  fontSize: '$3',
  color: '$muted',
});

const StyledButton = styled('button', {
  display: 'flex',
  alignItems: 'center',
  gap: '$xxs',
  background: 'none',
  border: 'none',
  color: 'inherit',
  cursor: 'pointer',
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
  const handleExportDiagnostics = () => {
    // TODO: Implement diagnostics export
    console.log('Export diagnostics clicked');
  };

  const handleViewLogs = () => {
    // TODO: Implement view logs
    console.log('View logs clicked');
  };

  return (
    <StyledFooter className={className} as="footer">
      <Flex css={{ alignItems: 'center', gap: '$sm', width: 'fit-content' }}>
        <StyledButton onClick={handleExportDiagnostics}>
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
        </StyledButton>
      </Flex>

      <Flex css={{ alignItems: 'center', gap: '$sm', width: 'fit-content' }}>
        <StyledLink
          href="https://github.com/ObolNetwork"
          target="_blank"
          rel="noopener noreferrer"
        >
          <IconWrapper>
            <GitHubLogoIcon />
          </IconWrapper>
          GitHub
        </StyledLink>
        <StyledLink
          href="https://discord.gg/obol"
          target="_blank"
          rel="noopener noreferrer"
        >
          <IconWrapper>
            <ChatBubbleIcon />
          </IconWrapper>
          Discord
        </StyledLink>
      </Flex>
    </StyledFooter>
  );
}
