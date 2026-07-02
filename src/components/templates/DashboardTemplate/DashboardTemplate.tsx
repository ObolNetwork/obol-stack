import { Box } from '@obolnetwork/obol-ui';
import { ReactNode } from 'react';
import { ToastContainer } from 'react-toastify';
import { PageContainer } from '@/components/atoms/PageContainer';

interface DashboardTemplateProps {
  header: ReactNode;
  content: ReactNode;
  footer?: ReactNode;
  /** Marketplace grids need the wider (1440px) container. */
  wide?: boolean;
}

export function DashboardTemplate({
  header,
  content,
  footer,
  wide = false,
}: DashboardTemplateProps) {
  return (
    <Box
      css={{
        minHeight: '100vh',
        backgroundColor: '$bg01',
        color: 'white',
        paddingTop: '$xl',
        paddingLeft: '$xl',
        paddingRight: '$xl',
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
      <PageContainer
        wide={wide}
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
      </PageContainer>
      <PageContainer wide={wide} css={{ flex: 1 }}>
        {content}
        <ToastContainer
          theme="colored"
          hideProgressBar={true}
          position="bottom-left"
          style={{ paddingBottom: 100 }}
        />
      </PageContainer>
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
