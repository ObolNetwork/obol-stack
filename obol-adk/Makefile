.PHONY: help setup start dev clean test

# Default target
help:
	@echo "Obol ADK - Development Commands"
	@echo ""
	@echo "Usage:"
	@echo "  make dev     - Setup gitbook + env and start agent (one command setup)"
	@echo "  make setup   - Clone/update obol-gitbook and configure .env"
	@echo "  make start   - Start the obol-agent-ag-ui agent"
	@echo "  make test    - Run agent tests"
	@echo "  make clean   - Remove obol-gitbook directory"
	@echo ""

# Setup development environment (clone gitbook, configure .env)
setup:
	@echo "Setting up development environment..."
	@python3 scripts/setup_dev.py

# Start the agent (assumes setup is done)
start:
	@echo "Starting Obol Agent AG-UI..."
	@cd obol-agent-ag-ui && source ../.venv/bin/activate && python agent.py

# Dev workflow: setup + start
dev: setup
	@echo ""
	@echo "Starting agent..."
	@cd obol-agent-ag-ui && source ../.venv/bin/activate && python agent.py

# Run tests
test:
	@echo "Running agent tests..."
	@cd obol-agent-ag-ui && source ../.venv/bin/activate && python test_agent.py

# Clean up gitbook directory
clean:
	@echo "Cleaning up obol-gitbook..."
	@rm -rf obol-gitbook
	@echo "✓ Cleaned"
