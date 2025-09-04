import { Box } from '@obolnetwork/obol-ui';
import { ReactNode } from 'react';

interface DashboardTemplateProps {
  header: ReactNode;
  content: ReactNode;
}

export function DashboardTemplate({ header, content }: DashboardTemplateProps) {
  return (
    <Box
      css={{
        minHeight: '100vh',
        backgroundColor: '$bg01',
        color: 'white',
        paddingTop: '100px',
        paddingLeft: '80px',
        paddingRight: '80px',
      }}
    >
      <Box css={{ position: 'relative', marginBottom: '40px' }}>{header}</Box>
      <Box>{content}</Box>
    </Box>
  );
}
