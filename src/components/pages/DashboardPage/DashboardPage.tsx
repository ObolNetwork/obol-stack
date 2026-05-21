import { DashboardTemplate } from '../../templates/DashboardTemplate';
import { NavigatedDashboardHeader } from '../../organisms/DashboardHeader';
import { DashboardContent } from '../../organisms/DashboardContent';
import { FooterBar } from '../../organisms/FooterBar';
import { LoadingState } from '../../molecules/LoadingState';
import { StackUnreachableState } from '../../molecules/StackUnreachableState';
import { useDashboardData } from '../../../hooks/useDashboardData';

export function DashboardPage() {
  const { data, loading, error, unreachable, refetch } =
    useDashboardData(12000); // Refresh every 12 seconds

  // Show loading state
  if (loading && !data) {
    return (
      <DashboardTemplate
        header={<NavigatedDashboardHeader />}
        content={<LoadingState message="Loading dashboard data..." />}
        footer={<FooterBar />}
      />
    );
  }

  // When the cluster isn't answering AND we have no cached data, take over
  // the whole content with the shared unreachable card. Once data has been
  // seen, fall through to DashboardContent so transient blips don't blow
  // the dashboard away on every poll.
  if (unreachable && !data) {
    return (
      <DashboardTemplate
        header={<NavigatedDashboardHeader />}
        content={
          <StackUnreachableState
            detail={error ?? undefined}
            onRetry={refetch}
          />
        }
        footer={<FooterBar />}
      />
    );
  }

  return (
    <DashboardTemplate
      header={<NavigatedDashboardHeader />}
      content={<DashboardContent data={data} error={error} onRetry={refetch} />}
      footer={<FooterBar />}
    />
  );
}
