# Obol Agent AG-UI Backend

AG-UI backend for Obol Agent with MCP tools integration.

## Quick Start with Docker

### Using Docker Compose (Recommended)

```bash
# Build and run
docker-compose up -d

# View logs
docker-compose logs -f

# Stop
docker-compose down
```

The service will be available at `http://localhost:8000`.

### Using Docker Directly

```bash
# Build the image
docker build -t obol-agent-ag-ui:latest .

# Run the container
docker run -d \
  --name obol-agent-ag-ui \
  -p 8000:8000 \
  -e LLM_MODEL=gemini-2.0-flash \
  obol-agent-ag-ui:latest
```

## Configuration

Copy `.env.example` to `.env` for custom configuration:

```bash
cp .env.example .env
```

Environment variables:
- `PORT` - Server port (default: 8000)
- `LLM_MODEL` - LLM model to use (default: gemini-2.0-flash)
- `LOG_REQUESTS` - Enable request logging (default: false)
- `ENABLE_MCP_SERVERS` - Enable MCP server integrations (default: false in containers)

## API Endpoints

- `/` - AG-UI agent endpoint (POST)
- `/health` - Health check (GET)
- `/info` - Agent information (GET)

## Local Development

### Setup

```bash
pip install -r requirements.txt
```

### Run

```bash
# With local MCP servers (original version)
python agent.py

# Container-friendly version
python agent_container.py
```

Server runs on `http://localhost:8000` with AG-UI endpoint at `/`.

## Frontend Integration

Connect your AG-UI frontend to `http://localhost:8000/`.

## Container Details

The Docker image:
- Uses Python 3.13 slim base
- Runs as non-root user (`obol-agent`)
- Includes health checks
- Exposes port 8000
- Supports both standalone and compose deployment