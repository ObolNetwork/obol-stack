# Obol Stack Dashboard Deployment

## Custom Domain Setup

This dashboard is configured to serve on `http://obol.stack` with monitoring links to `http://monitoring.obol.stack`.

### Local Development

```bash
# Add to /etc/hosts for local testing
127.0.0.1 obol.stack
127.0.0.1 monitoring.obol.stack

# Start development server
npm run dev

# Or for localhost development
npm run dev:local
```

### Production Deployment

#### Option 1: Docker Container (Standalone)

The Docker container runs Next.js in standalone mode and serves the application directly:

```bash
# Using Docker Compose (Recommended)
docker-compose up -d

# Or manual Docker build and run
docker build -t obol-stack-front-end .
docker run -d \
  --name obol-stack-dashboard \
  -p 80:3000 \
  -e NODE_ENV=production \
  obol-stack-front-end
```

#### Option 2: Docker + Nginx Reverse Proxy

For production with SSL termination, rate limiting, and static asset caching:

```bash
# Run the Next.js container
docker run -d \
  --name obol-stack-app \
  -p 3000:3000 \
  -e NODE_ENV=production \
  obol-stack-front-end

# Use the provided nginx.conf as a reverse proxy
# (See nginx configuration section below)
```

#### Option 3: Native Node.js

```bash
# Install dependencies
npm install

# Build the application
npm run build

# Start production server
npm run start
```

### Network Configuration

#### Direct Container Access
The Docker container exposes port 3000 and can serve traffic directly:
- Container serves on port 3000
- Map to host port 80: `-p 80:3000`
- Access directly at `http://obol.stack`

#### With Reverse Proxy (Recommended for Production)
For production deployments, use nginx or similar reverse proxy:
1. Route `obol.stack` traffic to container (port 3000)
2. Route `monitoring.obol.stack` traffic to your monitoring stack
3. Handle SSL termination at proxy level
4. Enable static asset caching and compression

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_ENV` | `development` | Application environment |
| `PORT` | `3000` | Port to serve the application |
| `HOSTNAME` | `0.0.0.0` | Hostname to bind to |

## Reverse Proxy Configuration (Optional)

The included `nginx.conf` provides a production-ready reverse proxy configuration.

### When to Use Nginx Reverse Proxy

✅ **Use nginx when you need:**
- SSL/TLS termination
- Static asset caching
- Rate limiting
- Load balancing
- Compression (gzip/brotli)
- Security headers
- Multiple services on same domain

❌ **Skip nginx if you have:**
- Simple single-container deployment
- Cloud load balancer handling SSL
- Container orchestration (Kubernetes)
- Another reverse proxy already in place

### Using the Provided nginx.conf

```bash
# Copy nginx configuration
sudo cp deployment/nginx.conf /etc/nginx/sites-available/obol-stack
sudo ln -s /etc/nginx/sites-available/obol-stack /etc/nginx/sites-enabled/

# Test and reload nginx
sudo nginx -t
sudo systemctl reload nginx
```

### Docker Compose with Nginx

```yaml
version: '3.8'
services:
  app:
    build: .
    ports:
      - "3000:3000"
    
  nginx:
    image: nginx:alpine
    ports:
      - "80:80"
    volumes:
      - ./deployment/nginx.conf:/etc/nginx/conf.d/default.conf
    depends_on:
      - app
```

### Health Checks

#### Container Health Check
```bash
# Basic health check
curl -f http://localhost:3000 || exit 1

# With Docker
docker run --health-cmd="curl -f http://localhost:3000 || exit 1" \
           --health-interval=30s \
           --health-timeout=10s \
           --health-retries=3 \
           obol-stack-front-end
```

#### Application Status
The dashboard displays real-time node and system status. For monitoring integration:
- Root path `/` shows the dashboard
- Static assets cached by nginx (if used)
- Application logs available via `docker logs`