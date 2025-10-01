'use client';
import { DashboardPage } from '../components';
import { CopilotSidebar } from '@copilotkit/react-ui';
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
      <CopilotSidebar
        defaultOpen={false}
        clickOutsideToClose={true}
        labels={{
          title: 'Obol Assistant',
          initial:
            "👋 Hi! I'm your Obol ADK Agent.\n\nI can help you with:\n- Monitoring your Obol Stack\n- Understanding node metrics\n- Troubleshooting issues\n- Explaining system status\n\nHow can I assist you today?",
        }}
      />
    </div>
  );
}
