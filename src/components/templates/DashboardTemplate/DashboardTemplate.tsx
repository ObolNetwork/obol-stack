import { Box } from '@obolnetwork/obol-ui';
import { ReactNode } from 'react';

interface DashboardTemplateProps {
  header: ReactNode;
  content: ReactNode;
  footer?: ReactNode;
}

export function DashboardTemplate({
  header,
  content,
  footer,
}: DashboardTemplateProps) {
  return (
    <Box
      css={{
        minHeight: '100vh',
        backgroundColor: '$bg01',
        color: 'white',
        paddingTop: '$6xl',
        paddingLeft: '$5xl',
        paddingRight: '$5xl',
        paddingBottom: footer ? '$2xl' : '$5xl',
        display: 'flex',
        flexDirection: 'column',

        // Responsive design for smaller screens
        '@media (max-width: 768px)': {
          paddingLeft: '$md',
          paddingRight: '$md',
          paddingTop: '$3xl',
          paddingBottom: footer ? '$lg' : '$3xl',
        },

        '@media (max-width: 480px)': {
          paddingLeft: '$sm',
          paddingRight: '$sm',
          paddingTop: '$2xl',
          paddingBottom: footer ? '$md' : '$2xl',
        },
      }}
    >
      <Box
        css={{
          position: 'relative',
          marginBottom: '$2xl',
          '@media (max-width: 768px)': {
            marginBottom: '$lg',
          },
          '@media (max-width: 480px)': {
            marginBottom: '$md',
          },
        }}
      >
        {header}
      </Box>
      <Box css={{ flex: 1 }}>{content}</Box>
      {footer && (
        <Box
          css={{
            marginTop: '$2xl',
            '@media (max-width: 768px)': {
              marginTop: '$lg',
            },
            '@media (max-width: 480px)': {
              marginTop: '$md',
            },
          }}
        >
          {footer}
        </Box>
      )}
    </Box>
  );
}
