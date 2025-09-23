import { DashboardTemplate } from '../../templates/DashboardTemplate';
import { DashboardHeader } from '../../organisms/DashboardHeader';
import { DashboardContent } from '../../organisms/DashboardContent';
import { FooterBar } from '../../organisms/FooterBar';
import { LoadingState } from '../../molecules/LoadingState';
import { ErrorState } from '../../molecules/ErrorState';
import { useDashboardData } from '../../../hooks/useDashboardData';
import { Box } from '@obolnetwork/obol-ui';

export function DashboardPage() {
  const { data, loading, error, refetch } = useDashboardData(12000); // Refresh every 12 seconds

  // Show loading state
  if (loading && !data) {
    return (
      <DashboardTemplate
        header={<DashboardHeader />}
        content={<LoadingState message="Loading dashboard data..." />}
        footer={<FooterBar />}
      />
    );
  }

  return (
    <DashboardTemplate
      header={<DashboardHeader />}
      content={
        <Box>
          {error && (
            <ErrorState
              message={`API connection failed. Using cached data. Error: ${error}`}
              onRetry={refetch}
              variant="banner"
            />
          )}
          {data && <DashboardContent data={data} />}
        </Box>
      }
      footer={<FooterBar />}
    />
  );
}
