# Obol ADK Dockerfiles

This directory contains the `Dockerfile` definitions for the various MCP (Model-Context-Protocol) servers used by the Obol Agent Development Kit (ADK).

## Foundry MCP Server (`Dockerfile.foundry`)

This Dockerfile builds the image for the Foundry MCP server, which allows the Obol Agent to interact with the Foundry development toolkit.

### Building the Image

To ensure that the Docker build process pulls the latest changes from the `foundry-mcp-server` repository, you must use a build argument to pass the latest commit hash. This prevents Docker from using a stale cache.

Run the following command from the root of the `obol-stack` repository to build the image:

```bash
docker build \
  --build-arg REPO_COMMIT_HASH=$(git ls-remote https://github.com/PraneshASP/foundry-mcp-server.git HEAD | cut -f1) \
  -f obol-adk/dockerfiles/Dockerfile.foundry \
  -t foundry-mcp-server:latest \
  .
```

This command:
1.  Fetches the latest commit hash from the `HEAD` of the `foundry-mcp-server` repository.
2.  Passes it as the `REPO_COMMIT_HASH` build argument.
3.  Specifies the `Dockerfile.foundry` file.
4.  Tags the resulting image as `foundry-mcp-server:latest`.
5.  Uses the current directory (`.`) as the build context.
