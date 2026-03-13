FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Install uv for running the autoresearch repo environment.
RUN curl -LsSf https://astral.sh/uv/install.sh | sh \
    && mv /root/.local/bin/uv /usr/local/bin/uv

WORKDIR /app

# Copy the worker API script. Build context should be the skill directory:
#   docker build -f docker/Dockerfile -t autoresearch-worker ..
COPY scripts/worker_api.py /app/worker_api.py

ENV DATA_DIR=/data \
    AUTORESEARCH_REPO=/data/autoresearch \
    EXPERIMENT_TIMEOUT_SECONDS=300 \
    TRAIN_COMMAND="uv run train.py"

VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["python3", "/app/worker_api.py", "serve", "--repo", "/data/autoresearch", "--data-dir", "/data", "--host", "0.0.0.0", "--port", "8080"]
