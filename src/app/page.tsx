'use client';
import { DashboardPage, AgentSidebar } from '../components';
import { CopilotKitCSSProperties } from '@copilotkit/react-ui';

export default function ObolStackDashboard() {
  return (
    <div
      style={
        {
          '--copilot-kit-primary-color': '#2FE4AB',
        } as CopilotKitCSSProperties
      }
    >
      <DashboardPage />
      <AgentSidebar />
    </div>
  );
}
