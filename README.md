# Obol Stack Front End

A modern monitoring dashboard for Obol Stack and Ethereum node status, built with Next.js and styled to match the Obol Launchpad design system.

![Dashboard Preview](https://via.placeholder.com/800x400?text=Obol+Stack+Dashboard+Preview)

## Features

- 🎯 **Real-time Monitoring**: Track Ethereum execution and consensus clients
- 📊 **System Metrics**: Monitor disk usage, memory, CPU load, and uptime
- 🌐 **Chain Status**: View network participation and finalization status
- 🔧 **Port Status**: Monitor P2P, JSON-RPC, Prometheus, and Beacon API ports
- 🎨 **Authentic Styling**: Matches Obol Launchpad design system
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
npm install

# Start development server
npm run dev

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
npm run dev

# Production
npm run start
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_ENV` | `development` | Application environment |
| `PORT` | `3000` | Port to serve the application |
| `HOSTNAME` | `0.0.0.0` | Hostname to bind to |
| `NEXT_TELEMETRY_DISABLED` | `1` | Disable Next.js telemetry |

## Deployment

See [deployment/README.md](deployment/README.md) for detailed deployment instructions including:

- Docker container deployment
- Nginx reverse proxy setup
- SSL/TLS configuration
- Health checks and monitoring

## Development

### Tech Stack

- **Framework**: Next.js 15.4.5 with React 19
- **Styling**: Tailwind CSS v4 with custom Obol color system
- **TypeScript**: Full type safety
- **Icons**: Lucide React
- **Font**: DM Sans (Obol brand font)

### Available Scripts

```bash
npm run dev          # Development server
npm run dev:local    # Development server (localhost)
npm run build        # Production build
npm run start        # Production server
npm run start:local  # Production server (localhost)
npm run lint         # ESLint
```

### Project Structure

```
src/
├── app/
│   ├── globals.css      # Tailwind CSS and custom styles
│   ├── layout.tsx       # Root layout with DM Sans font
│   └── page.tsx         # Main dashboard component
├── components/          # Reusable components (future)
└── lib/                 # Utilities and helpers (future)
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
- Follow Obol design system conventions
- Include tests for new functionality
- Update documentation as needed

## Architecture

### Design System

The dashboard uses authentic Obol branding:

- **Colors**: Extracted from `blog.obol.org/branding`
- **Typography**: DM Sans font family
- **Styling**: Based on Obol Launchpad patterns
- **Components**: Tailwind CSS with custom Obol color system

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
- [Discord](https://discord.gg/obol)
- [GitHub](https://github.com/ObolNetwork)

## Support

For questions and support:

- 📖 [Documentation](https://docs.obol.org)
- 💬 [Discord Community](https://discord.gg/obol)
- 🐛 [Issue Tracker](https://github.com/ObolNetwork/obol-stack-front-end/issues)
- 📧 [Email Support](mailto:support@obol.org)
