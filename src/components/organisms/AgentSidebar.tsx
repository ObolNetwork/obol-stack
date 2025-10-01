'use client';

import { useEffect, useState } from 'react';
import { CopilotSidebar } from '@copilotkit/react-ui';

export function AgentSidebar() {
  const [isMounted, setIsMounted] = useState(false);

  useEffect(() => {
    setIsMounted(true);
  }, []);

  if (!isMounted) {
    return null;
  }

  return (
    <CopilotSidebar
      defaultOpen={false}
      clickOutsideToClose={true}
      labels={{
        title: 'Obol Assistant',
        initial:
          "👋 Hi! I'm your Obol ADK Agent.\n\nI can help you with:\n- Monitoring your Obol Stack\n- Understanding node metrics\n- Troubleshooting issues\n- Explaining system status\n\nHow can I assist you today?",
      }}
    />
  );
}
