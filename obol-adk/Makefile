.PHONY: help install setup start start-web dev clean test ci deploy

# Use absolute path for venv to work from subdirectories
VENV := $(CURDIR)/.venv
PYTHON := $(VENV)/bin/python
PIP := $(VENV)/bin/pip
ADK := $(VENV)/bin/adk
AGENT_DIR := $(CURDIR)/obol-agent-ag-ui
WEB_AGENT_DIR := $(CURDIR)/obol-agent-web

# Default target
help:
	@echo "Obol ADK - Development Commands"
	@echo ""
	@echo "Usage:"
	@echo "  make dev       - Full setup (venv + gitbook + env) and start agent"
	@echo "  make install   - Create venv and install Python dependencies"
	@echo "  make setup     - Clone/update obol-gitbook and configure .env"
	@echo "  make start     - Start the obol-agent-ag-ui agent (FastAPI backend)"
	@echo "  make start-web - Start the obol-agent-web agent (ADK Web UI)"
	@echo "  make test      - Run agent unit tests"
	@echo "  make ci        - Run full CI workflow locally (simulates GitHub Actions)"
	@echo "  make deploy    - Deploy obol-agent-ag-ui to Google Cloud Run"
	@echo "  make clean     - Remove obol-gitbook directory"
	@echo ""

# Create virtual environment and install dependencies
install:
	@echo "Setting up Python virtual environment..."
	@if [ ! -d "$(VENV)" ]; then \
		python3 -m venv $(VENV); \
		echo "✓ Virtual environment created"; \
	else \
		echo "✓ Virtual environment already exists"; \
	fi
	@echo "Installing dependencies..."
	@$(PIP) install --upgrade pip
	@$(PIP) install -r requirements.txt
	@echo "✓ Dependencies installed"

# Setup development environment (clone gitbook, configure .env)
setup:
	@echo "Setting up development environment..."
	@$(PYTHON) scripts/setup_dev.py

# Start the agent (assumes install + setup are done)
start:
	@if [ ! -d "$(VENV)" ]; then \
		echo "✗ Virtual environment not found. Run 'make install' first."; \
		exit 1; \
	fi
	@echo "Starting Obol Agent AG-UI (FastAPI backend)..."
	@echo "Available at: http://localhost:8000/"
	@cd obol-agent-ag-ui && $(PYTHON) agent.py

# Start the web agent with ADK Web UI
start-web:
	@if [ ! -d "$(VENV)" ]; then \
		echo "✗ Virtual environment not found. Run 'make install' first."; \
		exit 1; \
	fi
	@echo "Starting Obol Agent Web UI..."
	@echo "This will launch the ADK Web interface"
	@echo ""
	@echo "Important: Make sure to select 'obol-agent-web' from the dropdown in the web UI"
	@echo ""
	@$(ADK) web

# Dev workflow: install + setup + start
dev: install setup
	@echo ""
	@echo "Starting agent..."
	@cd obol-agent-ag-ui && $(PYTHON) agent.py

# Run tests
test:
	@if [ ! -d "$(VENV)" ]; then \
		echo "✗ Virtual environment not found. Run 'make install' first."; \
		exit 1; \
	fi
	@echo "Running agent tests..."
	@cd obol-agent-ag-ui && $(PYTHON) test_agent.py

# Run CI workflow locally (simulates GitHub Actions)
ci:
	@if [ ! -d "$(VENV)" ]; then \
		echo "✗ Virtual environment not found. Run 'make install' first."; \
		exit 1; \
	fi
	@echo "==================================================================="
	@echo "Running CI workflow (simulates GitHub Actions)"
	@echo "==================================================================="
	@echo ""
	@echo "Step 1: Installing test dependencies..."
	@$(PIP) install requests > /dev/null 2>&1
	@echo "✓ Dependencies installed"
	@echo ""
	@echo "Step 2: Creating minimal .env..."
	@cd $(AGENT_DIR) && echo "FILESYSTEM_MCP_PATHS=" > .env
	@cd $(AGENT_DIR) && echo "GOOGLE_API_KEY=$${GOOGLE_API_KEY}" >> .env
	@echo "✓ .env created"
	@echo ""
	@echo "Step 3: Starting agent in background..."
	@cd $(AGENT_DIR) && (nohup $(PYTHON) agent.py > agent.log 2>&1 & echo $$! > agent.pid)
	@sleep 1
	@echo "✓ Agent started (PID: $$(cat $(AGENT_DIR)/agent.pid 2>/dev/null || echo 'unknown'))"
	@echo ""
	@echo "Step 4: Waiting for agent to start (10s)..."
	@sleep 10
	@echo "✓ Ready"
	@echo ""
	@echo "Step 5: Running health check..."
	@curl -f http://localhost:8000/health > /dev/null 2>&1 || (echo "✗ Health check failed" && cat $(AGENT_DIR)/agent.log && kill $$(cat $(AGENT_DIR)/agent.pid) 2>/dev/null || true && exit 1)
	@echo "✓ Health check passed"
	@echo ""
	@echo "Step 6: Running endpoint tests..."
	@cd $(AGENT_DIR) && $(PYTHON) test_agent.py || (echo "✗ Tests failed" && cat agent.log && kill $$(cat agent.pid) 2>/dev/null || true && exit 1)
	@echo "✓ Tests passed"
	@echo ""
	@echo "Step 7: Stopping agent..."
	@kill $$(cat $(AGENT_DIR)/agent.pid) 2>/dev/null || true
	@rm -f $(AGENT_DIR)/agent.pid $(AGENT_DIR)/agent.log
	@echo "✓ Agent stopped"
	@echo ""
	@echo "==================================================================="
	@echo "✓ CI workflow completed successfully!"
	@echo "==================================================================="

# Deploy to Google Cloud Run
deploy:
	@echo "Deploying obol-agent-ag-ui to Google Cloud Run..."
	@echo "Region: us-east4"
	@echo "Project: prj-d-playgrounds-f0cb"
	@cd $(AGENT_DIR) && gcloud builds submit \
		--config=cloudbuild.yaml \
		--region=us-east4 \
		--substitutions=SHORT_SHA=$$(git rev-parse --short HEAD)

# Clean up gitbook directory
clean:
	@echo "Cleaning up obol-gitbook..."
	@rm -rf obol-gitbook
	@echo "✓ Cleaned"
