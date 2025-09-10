import { DashboardTemplate } from '../../templates/DashboardTemplate';
import { DashboardHeader } from '../../organisms/DashboardHeader';
import { DashboardContent } from '../../organisms/DashboardContent';
import { FooterBar } from '../../organisms/FooterBar';
import { TimingProvider } from '../../shared/TimingContext';
import { useState } from 'react';
import { DashboardData } from '../../../types/dashboard';
import { mockDashboardData } from '../../../data/mockData';

export function DashboardPage() {
  const [dashboardData] = useState<DashboardData>(mockDashboardData);

  return (
    <TimingProvider blockInterval={12000} peerInterval={4000}>
      <DashboardTemplate
        header={<DashboardHeader />}
        content={<DashboardContent data={dashboardData} />}
        footer={<FooterBar />}
      />
    </TimingProvider>
  );
}
