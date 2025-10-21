FROM ghcr.io/astral-sh/uv:python3.13-bookworm-slim

WORKDIR /app

# Install Node.js for npx (filesystem MCP server) and git for cloning docs
RUN apt-get update && apt-get install -y --no-install-recommends \
    nodejs \
    npm \
    git \
    && rm -rf /var/lib/apt/lists/*

# Copy requirements first for better caching
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Create non-root user
RUN adduser --disabled-password --gecos "" obol-agent && \
    chown -R obol-agent:obol-agent /app

# Clone obol-gitbook documentation
RUN git clone --depth 1 https://github.com/ObolNetwork/obol-gitbook.git /app/obol-gitbook

# Copy application code
COPY . .

# Set ownership for non-root user
RUN chown -R obol-agent:obol-agent /app

# Switch to non-root user
USER obol-agent

# Set environment variables
ENV PATH="/home/obol-agent/.local/bin:$PATH"
ENV PORT=8000
ENV PYTHONUNBUFFERED=1
ENV FILESYSTEM_MCP_PATHS=/app/obol-gitbook
ENV PUBLIC_MODE=false

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:${PORT}/health')" || exit 1

# Run the AG-UI agent with published MCP packages
CMD ["python", "agent.py"]