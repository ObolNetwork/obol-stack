# Enhanced Obol API Reader - FastMCP Server

This project provides an enhanced [FastMCP](https://github.com/jlowin/fastmcp) server that acts as a read-only interface to the public [Obol Network API](https://docs.obol.org/api/what-is-this-api). It exposes key Obol API GET endpoints as callable **tools** accessible via the Model Context Protocol (MCP).

This enhanced version includes **caching**, **rate limiting**, **comprehensive logging**, and **server monitoring** features for production-ready deployments.

## ✨ Enhanced Features

- **🚀 Intelligent Caching**: Configurable TTL-based caching to reduce API calls
- **⚡ Rate Limiting**: Built-in request throttling to respect API limits  
- **📊 Logging & Monitoring**: Comprehensive logging with configurable levels
- **🔧 Environment Configuration**: Full configuration via environment variables
- **💡 Health Checks**: Docker health checks and server status monitoring
- **📦 Production Ready**: Docker containerization with optimized layers

## 🛠️ Available Tools

### Server Management
*   **`obol_mcp_server_status`**: Get server status, cache statistics, and configuration
*   **`obol_mcp_clear_cache`**: Clear the internal cache manually

### Obol API Tools  
*   **`obol_api_health`**: Checks the health status of the Obol API (`GET /v1/_health`)
*   **`obol_cluster_effectiveness`**: Retrieves effectiveness metrics for a specific DV Cluster by `lock_hash` (`GET /v1/effectiveness/{lockHash}`)
*   **`obol_lock_by_config_hash`**: Retrieves a DV Cluster Lock Object using its `config_hash` (`GET /v1/lock/configHash/{configHash}`)
*   **`obol_locks_by_network`**: Lists DV Cluster Lock Objects for a specified network with pagination, sorting, and filtering (`GET /v1/lock/network/{network}`)
*   **`obol_terms_signed_status`**: Checks if an Ethereum address has signed the latest Obol Terms and Conditions (`GET /v1/termsAndConditions/{address}`)

## ⚙️ Configuration

Environment variables for customizing server behavior:

| Variable | Description | Default |
|----------|-------------|---------|
| `OBOL_API_BASE_URL` | Obol API base URL | `https://api.obol.tech` |
| `OBOL_REQUEST_TIMEOUT` | Request timeout in seconds | `15.0` |
| `OBOL_CACHE_TTL` | Cache TTL in seconds | `300` (5 minutes) |
| `OBOL_RATE_LIMIT_DELAY` | Delay between requests in seconds | `0.1` (100ms) |
| `LOG_LEVEL` | Logging level (DEBUG/INFO/WARNING/ERROR) | `INFO` |

## 📦 Requirements

*   Python 3.11+
*   FastMCP >= 0.2.0
*   httpx >= 0.24.0
*   uvicorn >= 0.23.0

Dependencies are managed via `requirements.txt` for reproducible builds.

## 🚀 Quick Start

### Docker (Recommended)

```bash
# Build the enhanced image
docker build -t obol-mcp:enhanced .

# Run with default settings
docker run -i obol-mcp:enhanced

# Run with custom configuration
docker run -i \
  -e OBOL_CACHE_TTL=600 \
  -e LOG_LEVEL=DEBUG \
  -e OBOL_REQUEST_TIMEOUT=30.0 \
  obol-mcp:enhanced

# Check container health
docker ps --filter "ancestor=obol-mcp:enhanced"
```

### Python Development

```bash
# Install dependencies
pip install -r requirements.txt

# Run the server
python server.py

# Run with custom environment
OBOL_CACHE_TTL=600 LOG_LEVEL=DEBUG python server.py
```

## Usage

Once the server is running, you can interact with it using any MCP-compatible client.

**Example using the FastMCP Python client:**

```python
# client_example.py
import asyncio
from fastmcp import Client

# Point the client to the running server file
# (Ensure server.py is in the same directory or provide the full path)
client = Client("server.py")

async def main():
    async with client:
        print("Connected to Obol API Reader MCP Server.")

        # Example 1: Check API Health
        health = await client.call_tool("obol_api_health")
        print("\nAPI Health:")
        print(health)

        # Example 2: Get Cluster Effectiveness (Replace with a real lock hash)
        lock_hash = "0xYOUR_CLUSTER_LOCK_HASH_HERE" # Replace this!
        if lock_hash != "0xYOUR_CLUSTER_LOCK_HASH_HERE":
             effectiveness = await client.call_tool("obol_cluster_effectiveness", {"lock_hash": lock_hash})
             print(f"\nEffectiveness for {lock_hash}:")
             print(effectiveness)
        else:
            print(f"\nSkipping effectiveness check (replace lock_hash in client_example.py)")


        # Example 3: List locks on Holesky
        locks = await client.call_tool(
            "obol_locks_by_network",
            {"network": "holesky", "limit": 5} # Get first 5 locks
        )
        print("\nLocks on Holesky (limit 5):")
        print(locks)

        # Example 4: Check T&C status (Replace with a real address)
        address = "0xYOUR_ETHEREUM_ADDRESS_HERE" # Replace this!
        if address != "0xYOUR_ETHEREUM_ADDRESS_HERE":
            terms_status = await client.call_tool("obol_terms_signed_status", {"address": address})
            print(f"\nTerms Signed Status for {address}:")
            print(terms_status)
        else:
            print(f"\nSkipping T&C check (replace address in client_example.py)")


if __name__ == "__main__":
    asyncio.run(main())
```

Run the client example: `python client_example.py`

**Using with Claude Desktop:**

You can install this server directly into Claude Desktop using the FastMCP CLI:

```bash
fastmcp install server.py --name "Obol API Reader"
```

## Configuration

*   **API URL:** The server currently connects to the public Obol API at `https://api.obol.tech`. This is hardcoded in `server.py`.
*   **Authentication:** No API keys or authentication are required for the currently implemented read-only GET endpoints. Adding tools for POST/PUT/DELETE operations would require handling authentication.

## Future Work

*   Add tools corresponding to more Obol API GET endpoints.
*   Implement POST/PUT/DELETE endpoints as tools (requires handling authentication securely).
*   Add data processing or summarization tools on top of the raw API calls.
*   Represent GET endpoints as MCP Resources/Templates instead of Tools for semantic clarity.
*   Implement caching for API responses where appropriate.
*   Allow API base URL configuration via environment variables.

## License

*(Assuming Apache 2.0 like the parent FastMCP project. Adjust if needed)*

Licensed under the Apache License, Version 2.0. See the [LICENSE](https://github.com/jlowin/fastmcp/blob/main/LICENSE) file in the main FastMCP repository for details.