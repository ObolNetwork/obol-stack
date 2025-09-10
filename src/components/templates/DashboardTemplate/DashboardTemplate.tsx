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
      }}
    >
      <Box css={{ position: 'relative', marginBottom: '$2xl' }}>{header}</Box>
      <Box css={{ flex: 1 }}>{content}</Box>
      {footer && <Box css={{ marginTop: '$2xl' }}>{footer}</Box>}
    </Box>
  );
}
