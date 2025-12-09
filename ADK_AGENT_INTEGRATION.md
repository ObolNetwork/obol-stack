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
- **Error Handling**: Detects connection errors and returns appropriate error responses

### 2. Health Check API (`/src/app/api/copilotkit/health/route.ts`)

A dedicated endpoint to check agent availability:

- **Connection Testing**: Attempts to connect to the ADK agent URL
- **Timeout Protection**: 3-second timeout to prevent hanging requests
- **Availability Status**: Returns `available: true/false` based on connection success
- **Error Detection**: Distinguishes between connection failures and server errors

### 3. Layout Provider (`/src/app/layout.tsx`)

Wraps the entire application with CopilotKit provider:

- Configured with runtime URL pointing to `/api/copilotkit`
- Specifies the `obol_agent` for communication
- Loads CopilotKit CSS styles

### 4. Agent Health Hook (`/src/hooks/useAgentHealth.ts`)

React hook for checking agent availability:

- **Automatic Detection**: Checks agent health on component mount
- **Simple API**: Returns `isAvailable` boolean status
- **Error Handling**: Gracefully handles connection failures

### 5. Agent Sidebar Component (`/src/components/molecules/AgentSidebar/AgentSidebar.tsx`)

The main agent UI component:

- **Dynamic Initial Message**: Shows different messages based on agent availability
- **Agent Available**: Displays welcome message with capabilities
- **Agent Unavailable**: Shows initialization instructions with code-formatted command
- **Health Check Integration**: Uses `useAgentHealth` hook to detect agent status
- **Custom Header**: Displays node status indicator
- **Obol Branding**: Custom styling with Obol design system

### 6. Dashboard Page (`/src/app/page.tsx`)

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
- Dynamic messaging based on agent availability

### 🔍 Agent Health Detection

- Automatic health checking on component mount
- Real-time availability status
- User-friendly initialization instructions when agent is unavailable
- Health check API endpoint for status verification

## Environment Configuration

Add to your `.env.local`:

```bash
# Public URL for frontend (optional, used for display purposes)
NEXT_PUBLIC_ADK_AGENT_URL=http://localhost:8000

# Server-side agent URL (used by API routes)
ADK_AGENT_URL=http://127.0.0.1:8000/
```

**Note**:

- `ADK_AGENT_URL` is used server-side by the API routes and defaults to `http://127.0.0.1:8000/`
- `NEXT_PUBLIC_ADK_AGENT_URL` is optional and used for frontend display/logging
- The health check endpoint uses `ADK_AGENT_URL` to verify agent availability

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

**Agent Status Detection**:

- If the agent is **available**: You'll see a welcome message with agent capabilities
- If the agent is **unavailable**: You'll see initialization instructions: "Initialize your Obol Agent by running `obol agent init` in your command line interface to get started."

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
4. Check the health endpoint: `curl http://localhost:3000/api/copilotkit/health`
   - Should return `{"available": true}` if agent is reachable
   - Should return `{"available": false}` if agent is not reachable

### Agent shows initialization message

If you see "Initialize your Obol Agent by running `obol agent init`..." instead of the welcome message:

1. **Check if agent is running**:

   ```bash
   curl http://localhost:8000
   ```

2. **Verify environment variable**:

   ```bash
   # Check server-side variable (used by API)
   echo $ADK_AGENT_URL
   ```

3. **Test health check endpoint**:

   ```bash
   curl http://localhost:3000/api/copilotkit/health
   ```

4. **Check network connectivity**: Ensure the frontend can reach the agent URL from the server

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

## Health Check API

The health check endpoint (`/api/copilotkit/health`) provides a way to verify agent availability:

**Endpoint**: `GET /api/copilotkit/health`

**Response** (Agent Available):

```json
{
  "available": true,
  "agentUrl": "http://127.0.0.1:8000/"
}
```

**Response** (Agent Unavailable):

```json
{
  "available": false,
  "agentUrl": "http://127.0.0.1:8000/",
  "error": "Connection failed"
}
```

**Usage in Components**:

```typescript
import { useAgentHealth } from '@/hooks/useAgentHealth';

function MyComponent() {
  const { isAvailable } = useAgentHealth();

  if (!isAvailable) {
    return <div>Agent is not available</div>;
  }

  return <div>Agent is ready!</div>;
}
```

## Next Steps

1. ✅ Install dependencies
2. ✅ Create API route for agent communication
3. ✅ Wrap app with CopilotKit provider
4. ✅ Create agent UI page
5. ✅ Configure environment variables
6. ✅ Add health check endpoint
7. ✅ Implement dynamic messaging based on agent availability
8. 🔄 Test with your running ADK agent
9. 🚀 Customize for your Obol Stack use cases
