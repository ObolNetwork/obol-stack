# Dockerfile to run the Enhanced Obol MCP server
FROM python:3.11-slim

WORKDIR /server

# Copy requirements first for better Docker layer caching
COPY requirements.txt /server/

# Install dependencies
RUN pip install --no-cache-dir -r requirements.txt

# Copy server files
COPY . /server/

# Environment variables for configuration
ENV LOG_LEVEL=INFO
ENV OBOL_CACHE_TTL=300
ENV OBOL_REQUEST_TIMEOUT=15.0
ENV OBOL_RATE_LIMIT_DELAY=0.1

EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD python -c "import asyncio; import httpx; asyncio.run(httpx.get('http://localhost:8080/health', timeout=5))" || exit 1

CMD ["python", "/server/server.py"]
