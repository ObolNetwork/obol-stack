### Network Design

Create a networks/ directory in OBOL_CONFIG_DIR e.g

- networks/aztec
- networks/l1
- networks/base

each of these has a helmfile which configures the correct state in each case
they may make assumptions on certain endpoints which we can paper over by proxy
of the erpc

These "network" configs are embedded into the binary in
internal/embed/networks/<network> We can leverage this in order to build the cli

- `obol network list`, would just traverse the directory names in
  `internal/embed/networks`
- `obol network install <network>`, would copy
  `internal/embed/networks/<network>` to `OBOL_CONFIG_DIR/networks` and run
  `helmfile sync` on the network's helmfile
- `obol network delete <network>`, would remove said network from the
  OBOL_CONFIG_DIR/networks and remove any associated namespaces for each

NOTE: Just as easy we could call this command `obol chain` as network may be
conflated to mainnet/sepolia/hoodi

There may be certain quirks that need to be figured out in each case

- we have to specify what chain-id in nearly all cases and there are varying
  supports for these across different "networks"
- best attempts must be made to NOT configure specific networks in context of
  each other, communications should be proxied through erpc. Doing so via
  templating logic incurs a huge burden on configuration complexity

Each respective <network>/helmfile.yaml could define a constrained api for basic
templating of certain aspects of it's network. These could be easily surfaced as
overridable env vars and could even be more tangibly utilised if the file was
parseable and such vars constructed into dynamic cli args.

### ERPC

ERPC should be extracted into it's own helmfile chart. It should have sane
initial defaults for helios and 3rd party/obol fallback rpc's which would be the
initial state when first ran with `obol stack up`

Subsequent `obol network install <network>` should re-template and re-sync the
erpc resource with each new relevant endpoint.

Perhaps there can be some controller/detection mechanism that can allow a given
network configuration to subscribe/unsubscribe it's endpoint(s) which then
auto-updates erpc?

https://docs.erpc.cloud/config/projects/providers#repository, could be used to
hit such a service.
