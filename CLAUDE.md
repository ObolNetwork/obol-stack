# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Core Commands

- `npm install` - Install dependencies
- `npm run dev` - Start development server with Turbopack on custom domain
- `npm run dev:local` - Start development server with Turbopack on localhost:3000
- `npm run build` - Build for production (Next.js standalone output)
- `npm run start` - Start production server on obol.stack domain
- `npm run start:local` - Start production server on localhost:3000
- `npm run lint` - Run ESLint with Next.js TypeScript config

### Docker Commands

- `docker-compose up -d` - Start containerized application with health checks
- Access at `http://localhost` (port 80)
- Health check endpoint: `http://localhost/api/health` (if implemented)

### Custom Domain Setup

The application is configured to run on `obol.stack` domain. Add to `/etc/hosts`:

```
127.0.0.1 obol.stack
127.0.0.1 monitoring.obol.stack
```

## Architecture Overview

### Tech Stack

- **Framework**: Next.js 15.4.5 with React 19
- **Language**: TypeScript with strict mode
- **Styling**: Tailwind CSS v4 with custom Obol design system
- **UI Library**: @obolnetwork/obol-ui package
- **Icons**: Lucide React
- **Font**: DM Sans (Obol brand font)

### Design System

The application uses authentic Obol branding extracted from `blog.obol.org/branding`:

- **Primary Colors**: Obol Green (#2FE4AB), Blue (#162A40), Purple (#9167E4)
- **Background System**: Multi-layered dark theme with gradients
- **Typography**: DM Sans font family with font weights 400-700
- **Component Style**: Launchpad-inspired cards with hover effects and glassmorphism

### Project Structure

```
src/
├── app/
│   ├── globals.css      # Tailwind CSS config + Obol design system
│   ├── layout.tsx       # Root layout with DM Sans font loading
│   └── page.tsx         # Main dashboard integration
├── components/
│   ├── layout/          # Header and Footer components
│   ├── metrics/         # Network status, node overview, client status
│   ├── monitoring/      # Port status and system monitoring
│   ├── shared/          # Reusable UI components (StatusIndicator, ProgressBar)
│   └── index.ts         # Component exports
├── types/
│   └── dashboard.ts     # TypeScript interfaces for dashboard data
└── data/
    └── mockData.ts      # Mock data for development
```

### Key Components

- **Header**: Minimal navigation with network indicator (testnet/mainnet)
- **NetworkStatus**: Ethereum mainnet status with blocks, finalized epochs, participation rate
- **NodeOverview**: System metrics grid (disk, memory, CPU, uptime) with progress bars
- **ClientStatus**: Geth and Prysm client status cards with peer count
- **PortStatus**: Port monitoring table (P2P, JSON-RPC, Prometheus, Beacon API)
- **Footer**: Action buttons for diagnostics export and log viewing
- **Shared Components**: StatusIndicator dots, ProgressBar with color variants, MetricCard template

### Data Architecture

TypeScript interfaces in `src/types/dashboard.ts` define the data structure:

- **DashboardData**: Main interface with header, ethereum, nodeOverview, clients, ports
- **Network Status**: Blocks, finalized epochs, participation rate, distance to justified
- **System Metrics**: Disk/memory usage with percentages, CPU load, uptime hours
- **Client Status**: Geth/Prysm versions, peer counts, sync status
- **Port Monitoring**: P2P (30303), JSON-RPC (8545), Prometheus (9090), Beacon API (4000)
- **Status Types**: 'synced', 'syncing', 'error' with corresponding UI indicators

### Configuration

- **Next.js Config**: Custom domain rewrites, security headers, standalone output for Docker
- **TypeScript**: Path mapping with `ObolNetwork/*` and `@/*` aliases pointing to `./src/*`
- **ESLint**: Next.js core web vitals and TypeScript config with FlatCompat
- **Turbopack**: Enabled by default for faster development builds
- **Environment Variables**: NODE_ENV, PORT, HOSTNAME, NEXT_TELEMETRY_DISABLED

### Security Features

- Next.js security headers (X-Frame-Options: DENY, X-Content-Type-Options: nosniff)
- Standalone Docker output for containerized deployment
- Production-ready nginx configuration in deployment folder
- Docker health checks with wget monitoring

### Development Architecture Patterns

- **Mock Data Strategy**: Uses `src/data/mockData.ts` implementing `DashboardData` interface
- **Component Architecture**: Modular design with shared components (StatusIndicator, ProgressBar, Card)
- **Type Safety**: Comprehensive TypeScript interfaces in `src/types/dashboard.ts`
- **Styling Approach**: Tailwind CSS with Obol design tokens and dark theme
- **Client-Side Rendering**: Uses `'use client'` for interactive dashboard components

### Future Integration Points

The codebase is prepared for real data integration with:

- Ethereum execution client APIs (Geth JSON-RPC)
- Consensus client APIs (Prysm/Lighthouse Beacon API)
- System monitoring APIs (Prometheus metrics)
- Charon distributed validator middleware (marked as "Coming Soon")

## Development Patterns

### Adding New Components

1. Create component in appropriate `src/components/` subdirectory
2. Follow existing patterns: use TypeScript interfaces from `src/types/dashboard.ts`
3. Apply Obol design system styles and dark theme
4. Export from `src/components/index.ts` for clean imports
5. Use shared components (StatusIndicator, ProgressBar) for consistency

### Working with Mock Data

- Mock data structure defined in `src/data/mockData.ts`
- Implements `DashboardData` interface from `src/types/dashboard.ts`
- Replace mock data calls with real API calls for production integration
- Maintain interface compatibility when switching to real data

### Testing Strategy

- No test framework currently configured
- Consider Jest + React Testing Library for component testing
- Docker health checks provide basic application health monitoring
- Manual testing via development server or Docker container
