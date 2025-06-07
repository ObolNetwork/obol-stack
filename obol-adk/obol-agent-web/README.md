# Obol Agent Web Interface

A comprehensive AI agent for Obol Distributed Validator management accessible via Google ADK's web interface.

## 🚀 Features

The Obol Agent provides access to four powerful MCP (Model Context Protocol) servers:

### 🔗 Available Tools

1. **📁 Filesystem MCP** (Official MCP Community)
   - File operations and project management
   - Read/write files across the entire workspace
   - Directory navigation and file manipulation
   - Maintained by the [MCP community](https://github.com/modelcontextprotocol/servers/tree/main/src/filesystem) for reliability

2. **🌐 Enhanced Obol MCP**
   - Obol API integration with caching and rate limiting
   - Cluster effectiveness monitoring
   - Network status and lock management
   - Terms and conditions checking

3. **☸️ Kubernetes MCP** (Official MCP Community)
   - Container orchestration and cluster management
   - Pod, deployment, and service operations
   - Kubernetes resource monitoring
   - Automatic KUBECONFIG mounting for cluster access

4. **⚒️ Foundry MCP**
   - Smart contract development and testing
   - Forge build, test, and deployment tools
   - Solidity development support

## ⚙️ Configuration

### Environment Variables

Copy `.env.example` to `.env` and customize:

```bash
cp .env.example .env
```

Key configuration options:

| Variable | Description | Default |
|----------|-------------|---------|
| `OBOL_AGENT_MODEL` | LLM model to use | `gemini-2.0-flash` |
| `OBOL_WORKSPACE_PATH` | Project workspace directory | Current project path |
| `OBOL_DOCS_PATH` | Documentation directory | `workspace/obol-adk/docs` |
| `KUBECONFIG` | Kubernetes config file path | `~/.kube/config` |
| `OBOL_MCP_LOG_LEVEL` | Obol MCP logging level | `INFO` |
| `OBOL_CACHE_TTL` | Obol API cache TTL (seconds) | `300` |

## 🚀 Quick Start

### Prerequisites

1. **Docker** - All MCP servers run in containers
2. **Google ADK** - Installed via `pip install google-adk`
3. **Built MCP Images** - Run the build script or use our workflow

### Pull and Build Required Images

```bash
# Pull official MCP filesystem server (maintained by MCP community)
docker pull mcp/filesystem

# Build custom MCP servers
cd /path/to/obol-stack

# Kubernetes MCP  
docker build -f obol-adk/dockerfiles/Dockerfile.kubernetes -t kubernetes-mcp:latest obol-adk/dockerfiles

# Foundry MCP
docker build -f obol-adk/dockerfiles/Dockerfile.foundry -t foundry-mcp:latest obol-adk/dockerfiles

# Enhanced Obol MCP
docker build -f obol-adk/obol-agent-docker/Dockerfile -t obol-mcp:enhanced obol-adk/obol-agent-docker
```

### Launch Web Interface

```bash
# Navigate to project root
cd /path/to/obol-stack/obol-adk

# Start the web interface
adk web

# Or with custom configuration
OBOL_AGENT_MODEL=gemini-1.5-pro adk web
```

The web interface will be available at `http://localhost:8080`

## 🛠️ Usage Examples

### Obol Cluster Management
```
"Check the health of the Obol API and show me effectiveness metrics for mainnet clusters"
```

### Kubernetes Operations
```  
"List all pods in the default namespace and show me the status of my deployments"
```

### Smart Contract Development
```
"Create a new Foundry project and help me write a simple ERC20 token contract"
```

### File Management
```
"Show me the structure of the docs directory and help me create a new documentation file"
```

## 🔧 Advanced Configuration

### Custom Docker Mounts

Modify `agent.py` to add custom volume mounts:

```python
# Add custom mounts for specific use cases
"-v", "/custom/path:/mount/point",
```

### MCP Server Configuration

Configure individual MCP servers via environment variables:

```bash
# Obol MCP with debug logging and extended cache
OBOL_MCP_LOG_LEVEL=DEBUG OBOL_CACHE_TTL=600 adk web

# Custom Kubernetes config
KUBECONFIG=/path/to/custom/kubeconfig adk web
```

## 🏗️ Architecture

```
Web Browser
    ↓
Google ADK Web Interface (FastAPI)
    ↓  
Obol Agent (LlmAgent)
    ↓
MCPToolset Connections
    ↓
Docker MCP Servers (stdio)
    ↓
External APIs/Tools
```

## 🐛 Troubleshooting

### Common Issues

1. **Docker Images Not Found**
   ```bash
   # Rebuild images
   docker build -f obol-adk/dockerfiles/Dockerfile.filesystem -t filesystem-mcp:latest obol-adk/dockerfiles
   ```

2. **Kubernetes Access Issues**
   ```bash
   # Check kubeconfig
   kubectl cluster-info
   export KUBECONFIG=/path/to/working/kubeconfig
   ```

3. **Permission Issues**
   ```bash
   # Fix workspace permissions
   chmod -R 755 /path/to/workspace
   ```

4. **Agent Not Responding**
   ```bash
   # Check logs
   LOG_LEVEL=DEBUG adk web
   ```

### Logs and Debugging

- Set `LOG_LEVEL=DEBUG` for verbose logging
- Check Docker container logs: `docker logs <container_id>`
- Monitor MCP connections in the ADK web interface

## 🤝 Contributing

1. Fork the repository
2. Create your feature branch
3. Test with `adk web`
4. Submit a pull request

## 📝 License

Licensed under the same terms as the main project.