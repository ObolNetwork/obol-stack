# Quick Start: ADK Agent Integration

This guide will help you quickly get started with the ADK agent integration.

## Prerequisites

✅ Your FastAPI ADK agent running on `http://localhost:8000`

## Quick Start (3 Steps)

### 1. Install Dependencies

Already done! ✅ The following packages have been installed:

- `@copilotkit/react-ui` - UI components for agent interaction
- `@copilotkit/react-core` - Core hooks and providers
- `@copilotkit/runtime` - Runtime for agent communication
- `@ag-ui/client` - AG-UI protocol client

### 2. Start the Development Server

```bash
pnpm dev
```

The app will start on `http://localhost:3000`

### 3. Access the Agent Interface

Open your browser and navigate to:

**http://localhost:3000**

You'll see your Obol Stack Dashboard with an agent sidebar available. Click the chat icon to interact with the agent!

## What's Been Set Up

### 📁 File Structure

```
src/
├── app/
│   ├── api/
│   │   └── copilotkit/
│   │       ├── route.ts          # ✅ API endpoint for agent communication
│   │       └── health/
│   │           └── route.ts      # ✅ Health check endpoint
│   ├── page.tsx                   # ✅ Main dashboard with CopilotSidebar
│   └── layout.tsx                 # ✅ Updated with CopilotKit provider
├── components/
│   └── molecules/
│       └── AgentSidebar/
│           └── AgentSidebar.tsx  # ✅ Agent sidebar with dynamic messaging
└── hooks/
    └── useAgentHealth.ts          # ✅ Hook for checking agent availability
```

### 🔧 Configuration

The following environment variables can be configured in `.env.local`:

```bash
# Server-side agent URL (used by API routes, required)
ADK_AGENT_URL=http://127.0.0.1:8000/

# Public URL for frontend (optional, for display/logging)
NEXT_PUBLIC_ADK_AGENT_URL=http://localhost:8000
```

**Note**: `ADK_AGENT_URL` defaults to `http://127.0.0.1:8000/` if not specified.

### 🎨 UI Components

The main dashboard page includes:

- **CopilotSidebar**: Interactive chat interface with Obol branding (opens on demand)
- **Obol Design System**: Custom styling with Obol green (#2FE4AB)
- **Integrated Experience**: Agent sidebar overlays the dashboard without disrupting your workflow
- **Dynamic Messaging**: Shows welcome message when agent is available, or initialization instructions when unavailable
- **Health Detection**: Automatically detects agent availability on component mount

## Testing the Integration

### 1. Verify Your ADK Agent is Running

```bash
curl http://localhost:8000
```

You should get a response from your FastAPI server.

### 2. Open the Dashboard

Navigate to `http://localhost:3000`

### 3. Interact with the Agent

Click the chat icon in the bottom right corner to open the agent sidebar and start chatting with your ADK agent!

**What you'll see**:

- **Agent Available**: Welcome message with agent capabilities
- **Agent Unavailable**: Instructions to run `obol agent init` to get started

## Next Steps

### Option 1: Use Shared State

Sync state between your app and the agent:

```typescript
import { useCoAgent } from '@copilotkit/react-core';

type AgentState = {
  selectedNode: string | null;
  metrics: Record<string, number>;
};

function YourComponent() {
  const { state, setState } = useCoAgent<AgentState>({
    name: 'adk_agent',
    initialState: {
      selectedNode: null,
      metrics: {},
    },
  });

  // State is now bidirectionally synced with your ADK agent!
}
```

### Option 2: Add Frontend Tools

Give your agent the ability to interact with the UI:

```typescript
import { useCopilotAction } from '@copilotkit/react-core';

useCopilotAction({
  name: 'highlight_metric',
  description: 'Highlight a specific metric in the dashboard',
  parameters: [{ name: 'metricName', type: 'string', required: true }],
  handler: async ({ metricName }) => {
    // Update your UI to highlight the metric
    setHighlightedMetric(metricName);
    return `Highlighted ${metricName}`;
  },
});
```

## Troubleshooting

### Agent Not Responding

1. Check if your ADK agent is running:

   ```bash
   curl http://localhost:8000
   ```

2. Verify agent health check:

   ```bash
   curl http://localhost:3000/api/copilotkit/health
   ```

   Expected response when agent is available:

   ```json
   { "available": true, "agentUrl": "http://127.0.0.1:8000/" }
   ```

3. Check browser console for errors (F12)

4. Verify the `ADK_AGENT_URL` in `.env.local` (server-side variable)

### Agent Shows Initialization Message

If the sidebar shows "Initialize your Obol Agent..." instead of the welcome message:

1. Ensure your ADK agent is running: `curl http://localhost:8000`
2. Check the health endpoint: `curl http://localhost:3000/api/copilotkit/health`
3. Verify `ADK_AGENT_URL` matches your agent's actual URL
4. Check network connectivity between frontend server and agent

### CORS Issues

If you see CORS errors, add CORS middleware to your FastAPI app:

```python
from fastapi.middleware.cors import CORSMiddleware

app.add_middleware(
    CORSMiddleware,
    allow_origins=["http://localhost:3000"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)
```

### Styles Not Loading

Make sure you're importing the CopilotKit styles in your layout:

```typescript
import '@copilotkit/react-ui/styles.css';
```

## Documentation

- **Full Integration Guide**: `ADK_AGENT_INTEGRATION.md`
- **CopilotKit Docs**: https://docs.copilotkit.ai/
- **AG-UI Protocol**: https://docs.ag-ui.com/
- **Google ADK**: https://google.github.io/adk-docs/

## Branch Information

This integration is on the `feature/adk-agent-integration` branch.

To merge to main:

```bash
git checkout main
git merge feature/adk-agent-integration
```

Happy building! 🚀
