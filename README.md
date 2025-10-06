# Obol Stack Front End

A modern monitoring dashboard for Obol Stack and Ethereum node status, built with Next.js and the official Obol UI Design System.

![Dashboard Preview](https://via.placeholder.com/800x400?text=Obol+Stack+Dashboard+Preview)

## Features

- 🎯 **Real-time Monitoring**: Track Ethereum execution and consensus clients (Nethermind, Nimbus)
- 📊 **System Metrics**: Monitor disk usage, memory, CPU load, and uptime
- 🌐 **Chain Status**: View network participation, finalization status, and sync progress
- 🔧 **Port Status**: Monitor P2P, JSON-RPC, Prometheus, and Beacon API ports
- 🎨 **Obol UI**: Built with official Obol Design System components
- 🧩 **Component Architecture**: Atomic design pattern for scalable development
- ♿ **Accessibility**: Radix UI primitives ensure WCAG compliance
- 🐳 **Docker Ready**: Containerized for easy deployment
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

| Variable                  | Default       | Description                   |
| ------------------------- | ------------- | ----------------------------- |
| `NODE_ENV`                | `development` | Application environment       |
| `PORT`                    | `3000`        | Port to serve the application |
| `HOSTNAME`                | `0.0.0.0`     | Hostname to bind to           |
| `NEXT_TELEMETRY_DISABLED` | `1`           | Disable Next.js telemetry     |

## Deployment

See [deployment/README.md](deployment/README.md) for detailed deployment instructions including:

- Docker container deployment
- Nginx reverse proxy setup
- SSL/TLS configuration
- Health checks and monitoring

## Development

### Tech Stack

- **Framework**: Next.js 15.5.2 with React 19
- **Styling**: Tailwind CSS v4 with custom Obol color system
- **Components**: Radix UI primitives for accessibility
- **TypeScript**: Full type safety
- **Architecture**: Atomic Design Pattern
- **Font**: DM Sans (Obol brand font)

### Available Scripts

```bash
pnpm run dev          # Development server
pnpm run dev:local    # Development server (localhost)
pnpm run build        # Production build
pnpm run start        # Production server
pnpm run start:local  # Production server (localhost)
pnpm run lint         # ESLint
pnpm run format       # Prettier formatting
pnpm run format:check # Check Prettier formatting
```

### Project Structure

```
src/
├── app/
│   ├── globals.css      # Global styles and resets
│   ├── layout.tsx       # Root layout with DM Sans font
│   └── page.tsx         # Main dashboard page
├── components/
│   ├── atoms/           # Basic building blocks
│   │   └── Accordion/   # Custom accordion component
│   ├── molecules/       # Simple combinations of atoms
│   ├── organisms/       # Complex UI components
│   │   ├── DashboardHeader/     # Header with logo
│   │   ├── DashboardContent/    # Main content area
│   │   └── NetworkStatus/       # Node monitoring component
│   ├── pages/           # Page-level components
│   │   └── DashboardPage/       # Main dashboard page
│   ├── templates/       # Page layout templates
│   │   └── DashboardTemplate/   # Dashboard layout
│   └── index.ts         # Component exports
└── lib/                 # Utilities and helpers
```

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

Currently uses mock data. Future versions will integrate with:

- Ethereum execution client APIs
- Consensus client APIs
- System monitoring APIs
- Charon distributed validator middleware

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
