import { DashboardTemplate } from '../../templates/DashboardTemplate';
import { DashboardHeader } from '../../organisms/DashboardHeader';
import { DashboardContent } from '../../organisms/DashboardContent';
import { FooterBar } from '../../organisms/FooterBar';
import { TimingProvider } from '../../shared/TimingContext';
import { useState, useEffect } from 'react';
import { DashboardData } from '../../../types/dashboard';
import { mockDashboardData } from '../../../data/mockData';

export function DashboardPage() {
  const [dashboardData, setDashboardData] =
    useState<DashboardData>(mockDashboardData);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    // Fetch initial data
    const fetchData = async () => {
      try {
        const response = await fetch('/api/dashboard');
        if (response.ok) {
          const data = await response.json();
          setDashboardData(data);
        }
      } catch (error) {
        console.error('Failed to fetch dashboard data:', error);
      } finally {
        setLoading(false);
      }
    };

    fetchData();

    // Set up polling for real-time updates
    const interval = setInterval(fetchData, 10000); // Update every 10 seconds

    return () => clearInterval(interval);
  }, []);

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
