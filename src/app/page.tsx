'use client';

import React, { useState } from 'react';
import { Header } from '../components/layout/Header';
import { NetworkStatus } from '../components/metrics/NetworkStatus';
import { NodeOverview } from '../components/metrics/NodeOverview';
import { FooterBar } from '../components/layout/FooterBar';
import { TimingProvider } from '../components/shared/TimingContext';
import { mockDashboardData } from '../data/mockData';
import { DashboardData } from '../types/dashboard';

export default function ObolStackDashboard() {
  const [dashboardData] = useState<DashboardData>(mockDashboardData);

  return (
    <TimingProvider blockInterval={12000} peerInterval={4000}>
      <div className="min-h-screen">
        <div className="container mx-auto py-6">
          <Header 
            network={dashboardData.header.network} 
            nodeStatus={dashboardData.header.nodeStatus}
          />

          <div className="grid grid-cols-1 xl:grid-cols-12 gap-6 mt-6">
            <section className="xl:col-span-8 space-y-6">
              <NetworkStatus data={dashboardData.ethereum} clients={dashboardData.clients} />
            </section>

            <aside className="xl:col-span-4 space-y-6">
              <NodeOverview data={dashboardData.nodeOverview} ports={dashboardData.ports} />
            </aside>
          </div>

          <FooterBar className="mt-8" />
        </div>
      </div>
    </TimingProvider>
  );
}