---

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

<!-- - Need to add .workspace or .local/bin to path automatically -->

<!-- - Need a longer timeout for k3d for lower resource computers -->

<!-- - Add observer in obol stack up to open browser when obol-frontend starts -->

- For add to wallet button, the erpc url is needed, environment ERPC_URL

<!-- - prometheus needs to be installed for system metrics + an ingress for the -->
<!-- frontend to hit -->

<!-- - production path in obolup.sh is broken -->
<!-- - production binary needs release -->

<!-- - full-node + light-client + 3rd party historical rpc -->

<!-- - do docker detection for users -->

<!-- - ** we need github action to construct obol releases -->

- We need to put defaults resources in an "obol-defaults" template
  - Similar maybe for "obol-full-nodes"
- Change readme inline with new changes
- Ensure helm repo adds ethpandaops and obol charts
- Fix the template issue where Values.network isn't being passed to the helmfile
- Don't wire https://obol.stack/rpc -> /rpc/mainnet
  - https://obol.stack/rpc/mainnet -> /rpc/mainnet
- obol.yaml in root
  - There should be a supported networks list in obol.yaml
  - We then have an empty "networks" list which a user may extend
  - The helmfile usage will map across this where necessary
- agent skeleton
- For full-nodes, inherit obol.yaml in full-nodes/helmfile
  - obol networks init, dumps the configuration into the config folder
  - obol networks add, appends to the networks list
  - obol networks sync, syncs against the helmfile.yaml
  - obol networks available, lists all available networks
  - obol networks installed, lists all installed networks
- fix log duplication

<!-- export NICKNAME=patrick export -->
<!-- OPERATOR_ADDRESS=0xe628702DC6bC7c751417D6f30fA8Ec91E906dCAE -->

<!-- obol helm repo add obol https://obolnetwork.github.io/helm-charts -->

<!-- obol helm install test-dv-pod obol/dv-pod --set -->
<!-- 'charon.fallbackBeaconNodeEndpoints[0]=https://ethereum-hoodi-beacon-api.publicnode.com' -->
<!-- --set charon.dkgSidecar.apiEndpoint=https://obol-api-nonprod-dev.dev.obol.tech -->
<!-- --set chainId=560048 --set -->
<!-- charon.operatorAddress=$OPERATOR_ADDRESS --set charon.dkgSidecar.image.tag=90a1656 --set charon.nickname=$NICKNAME -->
<!-- --set validatorClient.type=prysm --set -->
<!-- validatorClient.config.prysm.acceptTermsOfUse=true --set -->
<!-- centralMonitoring.enabled=true --set-string -->
<!-- centralMonitoring.token='obolkvmusKH1SFvr/ruCzfj/Vlix3E49=59TdIH1BcTLecZabTv9SF=81HbtXucMNjwsJbFW=Z9ND7hD/GYPtz=B?TpWs2sJq9FIz-uN1-4DyR1l6J=HyC=d=Q!-?fHP' -->
