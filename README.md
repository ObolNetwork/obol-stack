# Obol Stack Front End

A modern monitoring dashboard for Obol Stack and Ethereum node status, built with Next.js and the official Obol UI Design System.

![Dashboard Preview](https://via.placeholder.com/800x400?text=Obol+Stack+Dashboard+Preview)

## Features

- 🎯 **Real-time Monitoring**: Track Ethereum execution and consensus clients (Nethermind, Nimbus, Helios, Aztec)
- 📊 **System Metrics**: Monitor disk usage, memory, CPU load, and uptime
- 🌐 **Chain Status**: View network participation, finalization status, and sync progress
- 🔧 **Port Status**: Monitor P2P, JSON-RPC, Prometheus, and Beacon API ports
- 🤖 **AI Agent Integration**: Interactive ADK agent with CopilotKit for intelligent assistance
- 🎨 **Obol UI**: Built with official Obol Design System components
- 🧩 **Component Architecture**: Atomic design pattern for scalable development
- ♿ **Accessibility**: Radix UI primitives ensure WCAG compliance
- 🐳 **Docker Ready**: Containerized for easy deployment
- ☸️ **Kubernetes Integration**: Native Kubernetes API integration for cluster monitoring
- 🔒 **Security Hardened**: Production-ready with security best practices

## Quick Start

### Docker (Recommended)

```bash
# Clone the repository
git clone https://github.com/ObolNetwork/obol-stack-front-end.git
cd obol-stack-front-end

# Start with Docker Compose
docker-compose up -d

# Access at http://localhost
```

### Development

```bash
# Install dependencies
pnpm install

# Start development server
pnpm run dev

# Access at http://localhost:3000
```

## Configuration

### Custom Domain Setup

The dashboard is configured to serve on `http://obol.stack`:

```bash
# Add to /etc/hosts for local testing
127.0.0.1 obol.stack
127.0.0.1 monitoring.obol.stack

# Development with custom domain
pnpm run dev

# Production
pnpm run start
```

### Environment Variables

| Variable                                | Default                                                                          | Description                                      |
| --------------------------------------- | -------------------------------------------------------------------------------- | ------------------------------------------------ |
| `NODE_ENV`                              | `development`                                                                    | Application environment                          |
| `PORT`                                  | `3000`                                                                           | Port to serve the application                    |
| `HOSTNAME`                              | `0.0.0.0`                                                                        | Hostname to bind to                              |
| `NEXT_TELEMETRY_DISABLED`               | `1`                                                                              | Disable Next.js telemetry                        |
| `ADK_AGENT_URL`                         | `http://127.0.0.1:8000/`                                                         | Server-side ADK agent URL (for API routes)       |
| `NEXT_PUBLIC_ADK_AGENT_URL`             | `http://127.0.0.1:8000/`                                                         | Public ADK agent URL (for display/logging)       |
| `NEXT_PUBLIC_PROMETHEUS_URL`            | `http://monitoring-kube-prometheus-prometheus.monitoring.svc.cluster.local:9090` | Prometheus server URL                            |
| `NEXT_PUBLIC_ERPC_URL`                  | `https://obol.stack/rpc`                                                         | Ethereum RPC endpoint URL                        |
| `NEXT_PUBLIC_KUBECONFIG`                | -                                                                                | Path to kubeconfig file (for local development)  |
| `NEXT_PUBLIC_WALLET_CONNECT_PROJECT_ID` | -                                                                                | WalletConnect project ID for wallet integration  |
| `KUBERNETES_SERVICE_HOST`               | -                                                                                | Auto-detected when running in Kubernetes cluster |

## Deployment

### Docker Deployment

See [deployment/README.md](deployment/README.md) for detailed deployment instructions including:

- Docker container deployment
- Nginx reverse proxy setup
- SSL/TLS configuration
- Health checks and monitoring

### Kubernetes Deployment

See [deployment/README.md](deployment/README.md) for Kubernetes deployment with Skaffold:

- Quick deployment with Skaffold
- Development mode with hot reload
- Manual deployment steps
- Port forwarding and access

## Additional Documentation

- **[Development Guide](DEVELOPMENT.md)**: Development setup and guidelines
- **[Agent Quick Start](QUICKSTART_AGENT.md)**: Quick start guide for ADK agent integration
- **[Agent Integration](ADK_AGENT_INTEGRATION.md)**: Complete ADK agent integration documentation
- **[Deployment Guide](deployment/README.md)**: Docker and Kubernetes deployment instructions

## Development

### Tech Stack

- **Framework**: Next.js 15.5.2 with React 19
- **Styling**: Stitches-based styling system with Obol Design System tokens
- **Components**: Radix UI primitives for accessibility
- **AI Agent**: CopilotKit with AG-UI protocol for ADK agent integration
- **State Management**: TanStack Query (React Query) for server state
- **Blockchain**: Wagmi + RainbowKit for wallet connectivity
- **Kubernetes**: @kubernetes/client-node for cluster monitoring
- **TypeScript**: Full type safety
- **Architecture**: Atomic Design Pattern
- **Font**: DM Sans (Obol brand font)

### Available Scripts

```bash
# Development
pnpm run dev              # Development server with Turbopack
pnpm run dev:local        # Development server (localhost)

# Production
pnpm run build            # Production build
pnpm run start            # Production server (obol.stack domain)
pnpm run start:local      # Production server (localhost)

# Code Quality
pnpm run lint             # Run ESLint
pnpm run lint:fix         # Run ESLint with auto-fix
pnpm run format           # Format code with Prettier
pnpm run format:check     # Check Prettier formatting

# Testing
pnpm run test:e2e         # Run all E2E tests
pnpm run test:e2e:ui      # Run E2E tests in UI mode (recommended)
pnpm run test:e2e:headed  # Run E2E tests with visible browser
pnpm run test:e2e:debug   # Debug E2E tests
pnpm run test:e2e:report  # View test report
pnpm run test:e2e:codegen # Generate test code
pnpm run test:e2e:chromium # Run tests in Chromium only
pnpm run test:e2e:firefox  # Run tests in Firefox only
pnpm run test:e2e:webkit   # Run tests in WebKit only
```

### Project Structure

```
src/
├── app/
│   ├── api/
│   │   ├── copilotkit/          # AI agent API routes
│   │   │   ├── route.ts         # Main agent communication endpoint
│   │   │   └── health/          # Agent health check endpoint
│   │   └── dashboard/           # Dashboard data API
│   │       └── route.ts         # Dashboard metrics endpoint
│   ├── globals.css               # Global styles and resets
│   ├── layout.tsx                # Root layout with CopilotKit provider
│   ├── page.tsx                  # Main dashboard page
│   └── providers.tsx             # React Query and Wagmi providers
├── components/
│   ├── atoms/                    # Basic building blocks
│   │   ├── Card/                 # Card component
│   │   ├── StatusIndicator/      # Status indicator dots
│   │   ├── Tooltip/              # Tooltip component
│   │   └── ...
│   ├── molecules/                # Simple combinations of atoms
│   │   ├── AgentSidebar/         # AI agent chat sidebar
│   │   ├── ClientCard/           # Client status card
│   │   ├── WalletConnection/     # Wallet connection UI
│   │   └── ...
│   ├── organisms/                # Complex UI components
│   │   ├── DashboardHeader/      # Header with logo
│   │   ├── DashboardContent/     # Main content area
│   │   ├── NetworkStatus/        # Network monitoring component
│   │   └── NodeOverview/         # System metrics overview
│   ├── pages/                    # Page-level components
│   │   └── DashboardPage/        # Main dashboard page
│   ├── templates/                # Page layout templates
│   │   └── DashboardTemplate/    # Dashboard layout
│   └── index.ts                  # Component exports
├── hooks/
│   ├── useAgentHealth.ts         # Agent availability hook
│   ├── useDashboardData.ts       # Dashboard data fetching hook
│   └── useTokenPrices.ts         # Token price fetching hook
├── lib/
│   ├── config/
│   │   ├── environment.ts        # Environment configuration
│   │   └── wagmi.ts              # Wagmi wallet configuration
│   ├── services/
│   │   ├── client-metrics.ts     # Client metrics aggregation
│   │   ├── kubernetes-api.ts     # Kubernetes API client
│   │   └── prometheus.ts         # Prometheus metrics client
│   └── utils/                    # Utility functions
└── types/
    └── dashboard.ts              # TypeScript type definitions
```

## AI Agent Integration

The dashboard includes an integrated AI agent powered by Google's Agent Development Kit (ADK) and CopilotKit. The agent provides intelligent assistance for monitoring and managing your Obol Stack.

### Quick Start

1. **Start your ADK agent** (if you have one):

   ```bash
   # Your ADK agent should be running on http://localhost:8000
   ```

2. **Configure environment variables** (optional):

   ```bash
   # .env.local
   ADK_AGENT_URL=http://127.0.0.1:8000/
   NEXT_PUBLIC_ADK_AGENT_URL=http://localhost:8000
   ```

3. **Access the agent**: Click the chat icon in the bottom right corner of the dashboard

### Features

- **Interactive Chat**: Real-time conversation with your ADK agent
- **Health Detection**: Automatic detection of agent availability
- **Dynamic Messaging**: Context-aware messages based on agent status
- **Shared State**: Bidirectional state synchronization between app and agent

### Documentation

- **[Quick Start Guide](QUICKSTART_AGENT.md)**: Get started with the agent integration in 3 steps
- **[Full Integration Guide](ADK_AGENT_INTEGRATION.md)**: Comprehensive documentation on architecture and features

## Testing

This project uses [Playwright](https://playwright.dev/) for end-to-end testing.

### Running Tests

```bash
# Run all tests
pnpm test:e2e

# Run tests with UI (recommended for development)
pnpm test:e2e:ui

# Run tests with visible browser
pnpm test:e2e:headed

# Debug tests
pnpm test:e2e:debug

# View test report
pnpm test:e2e:report

# Generate test code
pnpm test:e2e:codegen

# Run tests for specific browser
pnpm test:e2e:chromium
pnpm test:e2e:firefox
pnpm test:e2e:webkit
```

### Test Coverage

- **Functional Tests**: User flows and interactions
- **Accessibility Tests**: Keyboard navigation, ARIA, contrast
- **Visual Regression Tests**: Screenshot comparisons
- **API Tests**: Direct endpoint testing

## Contributing

We welcome contributions! Please see our contributing guidelines and:

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/amazing-feature`
3. Commit changes: `git commit -m 'Add amazing feature'`
4. Push to branch: `git push origin feature/amazing-feature`
5. Open a Pull Request

### Code Standards

- Follow existing code style and patterns
- Use TypeScript for all new code
- Follow Obol UI Design System conventions
- Use atomic design pattern for component organization
- Ensure accessibility with Radix UI primitives
- Include tests for new functionality
- Update documentation as needed

## Architecture

### Design System

The dashboard uses the official Obol UI Design System:

- **Components**: @obolnetwork/obol-ui package
- **Typography**: DM Sans font family (Obol brand standard)
- **Styling**: Stitches-based styling system with Obol tokens
- **Accessibility**: Radix UI primitives for WCAG compliance
- **Architecture**: Atomic design pattern (atoms → molecules → organisms → templates → pages)

### Data Flow

The dashboard integrates with multiple data sources:

- **Prometheus**: System metrics (CPU, memory, disk, uptime)
- **Kubernetes API**: Application instances, pod status, port monitoring
- **Client Metrics**: Aggregated metrics from execution and consensus clients
- **ADK Agent**: AI-powered assistance via AG-UI protocol

### Supported Clients

- **Execution Clients**: Nethermind, Helios
- **Consensus Clients**: Nimbus
- **Other**: Aztec clients

## Security

See [SECURITY.md](.github/SECURITY.md) for our security policy and reporting vulnerabilities.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Links

- [Obol Network](https://obol.org)
- [Documentation](https://docs.obol.org)
- [Launchpad](https://launchpad.obol.org)
- [Discord](https://discord.obol.org)
- [GitHub](https://github.com/ObolNetwork)

## Support

For questions and support:

- 📖 [Documentation](https://docs.obol.org)
- 💬 [Discord Community](https://discord.obol.org)
- 🐛 [Issue Tracker](https://github.com/ObolNetwork/obol-stack-front-end/issues)
- 📧 [Email Support](mailto:support@obol.org)
