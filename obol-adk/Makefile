.PHONY: help install setup start dev clean test

# Use absolute path for venv to work from subdirectories
VENV := $(CURDIR)/.venv
PYTHON := $(VENV)/bin/python
PIP := $(VENV)/bin/pip

# Default target
help:
	@echo "Obol ADK - Development Commands"
	@echo ""
	@echo "Usage:"
	@echo "  make dev      - Full setup (venv + gitbook + env) and start agent"
	@echo "  make install  - Create venv and install Python dependencies"
	@echo "  make setup    - Clone/update obol-gitbook and configure .env"
	@echo "  make start    - Start the obol-agent-ag-ui agent"
	@echo "  make test     - Run agent tests"
	@echo "  make clean    - Remove obol-gitbook directory"
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
	@echo "Starting Obol Agent AG-UI..."
	@cd obol-agent-ag-ui && $(PYTHON) agent.py

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

# Clean up gitbook directory
clean:
	@echo "Cleaning up obol-gitbook..."
	@rm -rf obol-gitbook
	@echo "✓ Cleaned"
