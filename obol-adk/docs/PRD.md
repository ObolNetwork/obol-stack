
# Agents 
- obol_agent Root Agent. Main coordinator
<!-- - evm_agent: Handles EVM based tasks
- dvt_agent: Handles DVT based tasks
- monitoring_agent: Handles monitoring tasks, checks kubernetes cluster health, services status, etc. -->

# Tools

Kubernetes
```
        "kubernetes": {
            "command": "npx",
            "args": [
              "-y",
              "kubernetes-mcp-server@latest"
            ]
        }
```

EVM Foundry
```
    "foundry": {
      "command": "node",
      "args": [
        "path/to/foundry-mcp-server/dist/index.js"
      ],
      "env" :{
        "RPC_URL": "https://eth.drpc.org",
      }
    }
```
