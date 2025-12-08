# ADK Agent Integration with AG-UI

This document describes the integration of Google's Agent Development Kit (ADK) agents with the Obol Stack frontend using the AG-UI protocol and CopilotKit.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    Next.js Frontend                          │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  CopilotKit Provider (layout.tsx)                     │  │
│  │  ┌─────────────────────────────────────────────────┐  │  │
│  │  │  Dashboard Page (/)                             │  │  │
│  │  │  - CopilotSidebar UI Component                  │  │  │
│  │  │  - Shared State Management                      │  │  │
│  │  │  - Generative UI                                │  │  │
│  │  └─────────────────────────────────────────────────┘  │  │
│  └───────────────────────────────────────────────────────┘  │
│                           ↕                                  │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  API Route (/api/copilotkit/route.ts)                │  │
│  │  - CopilotRuntime                                    │  │
│  │  - HttpAgent (AG-UI Client)                         │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                           ↕ HTTP (AG-UI Protocol)
┌─────────────────────────────────────────────────────────────┐
│              FastAPI Backend (localhost:8000)                │
│  ┌───────────────────────────────────────────────────────┐  │
│  │  ADK Agent (Google Agent Development Kit)            │  │
│  │  - Planning & Reasoning                              │  │
│  │  - Tool Use                                          │  │
│  │  - State Management                                  │  │
│  └───────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
```

## Key Components

### 1. API Route (`/src/app/api/copilotkit/route.ts`)

The API route creates a bridge between the frontend and your ADK agent:

- **CopilotRuntime**: Manages agent communication lifecycle
- **HttpAgent**: Connects to your FastAPI ADK endpoint via AG-UI protocol
- **ExperimentalEmptyAdapter**: Service adapter for single-agent setups

### 2. Layout Provider (`/src/app/layout.tsx`)

Wraps the entire application with CopilotKit provider:

- Configured with runtime URL pointing to `/api/copilotkit`
- Specifies the `adk_agent` for communication
- Loads CopilotKit CSS styles

### 3. Dashboard Page (`/src/app/page.tsx`)

The main dashboard interface with integrated ADK agent:

- **CopilotSidebar**: Pre-built chat UI component overlaying the dashboard
- **Obol Branding**: Custom styling with Obol green (#2FE4AB) theme
- **Real-time Communication**: Bidirectional streaming with the agent
- **Non-intrusive**: Opens on demand, doesn't disrupt dashboard workflow

## Features Enabled

### 🤖 Smart Agent Capabilities

- Multi-step planning and reasoning (powered by ADK)
- Tool use and external API integration
- State management and memory

### 🔄 Real-time Synchronization

- Bidirectional state sharing via AG-UI protocol
- Live streaming of agent responses
- Progress tracking and status updates

### 🎨 Rich User Interface

- Pre-built CopilotSidebar component
- Customizable with Obol design system
- Support for generative UI components

## Environment Configuration

Add to your `.env.local`:

```bash
NEXT_PUBLIC_ADK_AGENT_URL=http://localhost:8000
```

This configures the endpoint where your FastAPI ADK agent is running.

## Usage

### 1. Start Your ADK Agent

Make sure your FastAPI ADK agent is running on `http://localhost:8000`:

```bash
# In your ADK agent directory
python agent.py
# or
uvicorn agent:app --host 0.0.0.0 --port 8000
```

### 2. Start the Frontend

```bash
pnpm dev
```

### 3. Access the Agent Interface

Navigate to: `http://localhost:3000`

You'll see your Obol Stack Dashboard with an agent chat icon in the bottom right. Click it to open the sidebar and interact with your ADK agent.

## Extending the Integration

### Adding Shared State

Use the `useCoAgent` hook to sync state between your app and agent:

```typescript
import { useCoAgent } from '@copilotkit/react-core';

type AgentState = {
  nodes: string[];
  metrics: Record<string, number>;
};

function YourComponent() {
  const { state, setState } = useCoAgent<AgentState>({
    name: 'adk_agent',
    initialState: {
      nodes: [],
      metrics: {},
    },
  });

  // State is now synced with your ADK agent!
}
```

### Adding Generative UI

Use `useCopilotAction` to render custom UI for agent tool calls:

```typescript
import { useCopilotAction } from '@copilotkit/react-core';

useCopilotAction({
  name: 'get_node_status',
  description: 'Get the status of an Obol node',
  available: 'disabled',
  parameters: [
    { name: 'nodeId', type: 'string', required: true },
  ],
  render: ({ args }) => {
    return <NodeStatusCard nodeId={args.nodeId} />;
  },
});
```

### Adding Frontend Tools

Give your agent the ability to interact with the frontend:

```typescript
useCopilotAction({
  name: 'highlight_node',
  description: 'Highlight a specific node in the UI',
  parameters: [{ name: 'nodeId', type: 'string', required: true }],
  handler: async ({ nodeId }) => {
    // Update UI state to highlight the node
    setHighlightedNode(nodeId);
    return `Highlighted node ${nodeId}`;
  },
});
```

## Troubleshooting

### Agent not connecting

1. Verify your ADK agent is running: `curl http://localhost:8000`
2. Check the browser console for errors
3. Verify `ADK_AGENT_URL` in `.env.local`

### CopilotKit styles not loading

Make sure you've imported the styles in `layout.tsx`:

```typescript
import '@copilotkit/react-ui/styles.css';
```

### CORS errors

If you're running the agent on a different domain, you may need to configure CORS in your FastAPI app:

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

## Resources

- **AG-UI Protocol**: https://docs.ag-ui.com/
- **CopilotKit Docs**: https://docs.copilotkit.ai/
- **Google ADK**: https://google.github.io/adk-docs/
- **ADK + AG-UI Integration**: https://docs.copilotkit.ai/adk

## Next Steps

1. ✅ Install dependencies
2. ✅ Create API route for agent communication
3. ✅ Wrap app with CopilotKit provider
4. ✅ Create agent UI page
5. ✅ Configure environment variables
6. 🔄 Test with your running ADK agent
7. 🚀 Customize for your Obol Stack use cases
