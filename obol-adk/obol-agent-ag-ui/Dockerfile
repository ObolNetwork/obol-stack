FROM python:3.13-slim

WORKDIR /app

# Install system dependencies if needed for google-adk/ag-ui-adk
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    && rm -rf /var/lib/apt/lists/*

# Copy requirements first for better caching
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Create non-root user
RUN adduser --disabled-password --gecos "" obol-agent && \
    chown -R obol-agent:obol-agent /app

# Copy application code
COPY . .

# Switch to non-root user
USER obol-agent

# Set environment variables
ENV PATH="/home/obol-agent/.local/bin:$PATH"
ENV PORT=8000
ENV PYTHONUNBUFFERED=1

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD python -c "import urllib.request; urllib.request.urlopen('http://localhost:${PORT}/health')" || exit 1

# Run the container-friendly AG-UI agent
CMD ["sh", "-c", "python agent_container.py"]