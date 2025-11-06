- erpc, monitoring, frontend

- agent, was in obolup
  - obol agent
  - skeleton out the cmd
  - this should have a dummy manifest which templates a config map secret
  - obol agent init, gets the secret from google account

- frontend (default)
- erpc, helios (default)
- obol agent workings (default)

- monitoring

- full node (installable) - possibly via obol network cmd?
  - obol network init
    - creates an initial dir with mainnet defaults etc
  - obol network select
    - rm's the network dir, user selects execution, beacon clients + network,
      templates them and dumps to network folder
  - obol network sync
    - syncs the chart

<!-- - Need to change `helmfile apply` to `helmfile sync` -->

- Need to add .workspace or .local/bin to path automatically
- Need a longer timeout for k3d for lower resource computers
- Add observer in obol stack up to open browser when obol-frontend starts
- For add to wallet button, the erpc url is needed, environment ERPC_URL
- prometheus needs to be installed for system metrics + an ingress for the
  frontend to hit
- production path in obolup.sh is broken
  - production binary needs release
- full-node + light-client + 3rd party historical rpc
- do docker detection
