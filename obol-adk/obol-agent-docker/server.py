# server.py
import os
import logging
import asyncio
from datetime import datetime, timedelta
from typing import Optional, Dict, Any, List
import httpx
from fastmcp import FastMCP

# --- Configuration ---
OBOL_API_BASE_URL = os.getenv("OBOL_API_BASE_URL", "https://api.obol.tech")
REQUEST_TIMEOUT = float(os.getenv("OBOL_REQUEST_TIMEOUT", "15.0"))
CACHE_TTL_SECONDS = int(os.getenv("OBOL_CACHE_TTL", "300"))  # 5 minutes default
RATE_LIMIT_DELAY = float(os.getenv("OBOL_RATE_LIMIT_DELAY", "0.1"))  # 100ms between requests

# --- Logging Setup ---
logging.basicConfig(
    level=os.getenv("LOG_LEVEL", "INFO"),
    format="%(asctime)s - %(name)s - %(levelname)s - %(message)s"
)
logger = logging.getLogger("obol-mcp-server")

# --- Simple Cache Implementation ---
class SimpleCache:
    def __init__(self, ttl_seconds: int = CACHE_TTL_SECONDS):
        self.cache: Dict[str, Dict[str, Any]] = {}
        self.ttl_seconds = ttl_seconds
    
    def get(self, key: str) -> Optional[Dict[str, Any]]:
        if key in self.cache:
            cached_item = self.cache[key]
            if datetime.now() < cached_item["expires"]:
                logger.debug(f"Cache hit for key: {key}")
                return cached_item["data"]
            else:
                logger.debug(f"Cache expired for key: {key}")
                del self.cache[key]
        return None
    
    def set(self, key: str, data: Dict[str, Any]) -> None:
        self.cache[key] = {
            "data": data,
            "expires": datetime.now() + timedelta(seconds=self.ttl_seconds)
        }
        logger.debug(f"Cached data for key: {key}")
    
    def clear(self) -> None:
        self.cache.clear()
        logger.info("Cache cleared")

# --- Global Cache Instance ---
cache = SimpleCache()

# --- FastMCP Server Setup ---
mcp = FastMCP(
    name="Obol API Reader",
    instructions="Provides read-only access to certain Obol API endpoints via tools with caching and rate limiting.",
    dependencies=["httpx"]
)

# --- Helper Function for API Calls ---
async def _call_obol_api(endpoint: str, params: Optional[Dict[str, Any]] = None, use_cache: bool = True) -> Dict[str, Any]:
    """Helper function to make GET requests to the Obol API with caching and rate limiting."""
    
    # Create cache key from endpoint and params
    cache_key = f"{endpoint}:{str(sorted(params.items()) if params else 'no_params')}"
    
    # Check cache first if enabled
    if use_cache:
        cached_result = cache.get(cache_key)
        if cached_result is not None:
            return cached_result
    
    # Rate limiting
    await asyncio.sleep(RATE_LIMIT_DELAY)
    
    url = f"{OBOL_API_BASE_URL}{endpoint}"
    logger.info(f"Making API call to: {url}")
    
    async with httpx.AsyncClient() as client:
        try:
            response = await client.get(url, params=params, timeout=REQUEST_TIMEOUT)
            response.raise_for_status()
            
            # Parse response
            try:
                result = response.json()
                logger.debug(f"Successful API response for {endpoint}")
            except Exception:
                result = {"status_code": response.status_code, "content": response.text}
                logger.warning(f"Non-JSON response from {endpoint}")
            
            # Cache successful results
            if use_cache and "error" not in result:
                cache.set(cache_key, result)
            
            return result
            
        except httpx.HTTPStatusError as e:
            logger.error(f"HTTP error {e.response.status_code} for {endpoint}: {e.response.text}")
            error_detail = e.response.text
            try:
                error_detail = e.response.json()
            except Exception:
                pass
            return {
                "error": f"HTTP error {e.response.status_code} calling Obol API",
                "endpoint": endpoint,
                "details": error_detail
            }
        except httpx.RequestError as e:
            logger.error(f"Request error for {endpoint}: {str(e)}")
            return {
                "error": "Request error calling Obol API",
                "endpoint": endpoint,
                "details": str(e)
            }
        except Exception as e:
            logger.error(f"Unexpected error for {endpoint}: {str(e)}")
            return {
                "error": "An unexpected error occurred",
                "endpoint": endpoint,
                "details": str(e)
            }

# --- Tools based on Obol API GET Endpoints ---

# 0. Server Status Tool (Internal)
@mcp.tool("obol_mcp_server_status")
async def get_server_status() -> Dict[str, Any]:
    """
    Get the status of the Obol MCP server including cache statistics and configuration.
    """
    cache_stats = {
        "cache_size": len(cache.cache),
        "cache_ttl_seconds": cache.ttl_seconds,
        "cached_keys": list(cache.cache.keys()) if len(cache.cache) < 10 else f"{len(cache.cache)} keys (too many to list)"
    }
    
    config = {
        "api_base_url": OBOL_API_BASE_URL,
        "request_timeout": REQUEST_TIMEOUT,
        "rate_limit_delay": RATE_LIMIT_DELAY,
        "cache_ttl": CACHE_TTL_SECONDS
    }
    
    return {
        "server": "Obol MCP Server",
        "status": "running",
        "timestamp": datetime.now().isoformat(),
        "cache_stats": cache_stats,
        "configuration": config
    }

# 1. Cache Management Tool
@mcp.tool("obol_mcp_clear_cache")
async def clear_cache() -> Dict[str, Any]:
    """
    Clear the internal cache of the Obol MCP server.
    """
    cache.clear()
    return {
        "status": "success",
        "message": "Cache cleared successfully",
        "timestamp": datetime.now().isoformat()
    }

# 2. Health Check Tool
@mcp.tool("obol_api_health")
async def get_health() -> Dict[str, Any]:
    """
    Check the Obol API health status.
    Corresponds to GET /v1/_health.
    """
    return await _call_obol_api("/v1/_health", use_cache=False)  # Don't cache health checks

# 3. Cluster Effectiveness Tool
@mcp.tool("obol_cluster_effectiveness")
async def get_cluster_effectiveness(lock_hash: str) -> Dict[str, Any]:
    """
    Retrieve the effectiveness metrics for a specific Distributed Validator Cluster.
    Corresponds to GET /v1/effectiveness/{lockHash}.

    Args:
        lock_hash: The lock_hash calculated for the cluster lock.
    """
    if not lock_hash:
         return {"error": "lock_hash argument is required."}
    return await _call_obol_api(f"/v1/effectiveness/{lock_hash}")

# 4. Cluster Lock by Config Hash Tool
@mcp.tool("obol_lock_by_config_hash")
async def get_lock_by_config_hash(config_hash: str) -> Dict[str, Any]:
    """
    Retrieve a Distributed Validator Cluster Lock Object by its config_hash.
    Corresponds to GET /v1/lock/configHash/{configHash}.

    Args:
        config_hash: The config_hash calculated for the cluster configuration.
    """
    if not config_hash:
        return {"error": "config_hash argument is required."}
    return await _call_obol_api(f"/v1/lock/configHash/{config_hash}")

# 5. Locks by Network Tool
@mcp.tool("obol_locks_by_network")
async def get_locks_by_network(
    network: str,
    page: int = 0,
    limit: int = 100,
    sortBy: str = "", 
    sortOrder: str = "", 
    pool: str = "", 
    details: bool = False,
) -> Dict[str, Any]:
    """
    Retrieve a list of Distributed Validator Cluster Lock Objects for a given network,
    with optional pagination, sorting, and filtering.
    Corresponds to GET /v1/lock/network/{network}.

    Args:
        network: The network to retrieve clusters on (e.g., 'mainnet', 'holesky', 'sepolia').
        page: The page number to retrieve.
        limit: The number of cluster lock objects to return per page.
        sortBy: Field to sort by (e.g., 'avg_effectiveness').
        sortOrder: Sort order ('asc' or 'desc').
        pool: Filter by cluster type or pool.
        details: Flag to populate cluster definition information.
    """
    if not network:
        return {"error": "network argument is required."}
    params: Dict[str, Any] = { # Ensure type hint for params
        "page": page,
        "limit": limit,
        "details": str(details).lower(), # API expects 'true' or 'false' strings
    }
    # Add optional parameters only if they are provided and not empty
    if sortBy:
        params["sortBy"] = sortBy
    if sortOrder:
        params["sortOrder"] = sortOrder
    if pool:
        params["pool"] = pool

    return await _call_obol_api(f"/v1/lock/network/{network}", params=params)

# 6. Terms and Conditions Signed Status Tool
@mcp.tool("obol_terms_signed_status")
async def get_terms_signed_status(address: str) -> Dict[str, Any]:
    """
    Check if the given address has signed the latest Obol Terms and Conditions.
    Corresponds to GET /v1/termsAndConditions/{address}.

    Args:
        address: The Ethereum address to check.
    """
    if not address:
        return {"error": "address argument is required."}
    return await _call_obol_api(f"/v1/termsAndConditions/{address}")

# --- Main Execution Block ---
if __name__ == "__main__":
    logger.info("Starting Enhanced Obol API Reader MCP Server...")
    logger.info(f"Configuration: API_URL={OBOL_API_BASE_URL}, TIMEOUT={REQUEST_TIMEOUT}s, CACHE_TTL={CACHE_TTL_SECONDS}s")
    
    try:
        mcp.run()  # Runs with default stdio transport
    except KeyboardInterrupt:
        logger.info("Server shutdown requested")
    except Exception as e:
        logger.error(f"Server error: {e}")
        raise
    finally:
        logger.info("Obol MCP Server stopped")