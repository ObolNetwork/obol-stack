# Apps: Helm charts with `obol app`

`obol app` installs a Helm chart into the stack as a managed deployment: one
directory of declarative files on disk, synced to the cluster with helmfile and
replayed on `obol stack up`.

```
~/.config/obol/applications/<name>/<id>/
├── helmfile.yaml   chart, version, release name, namespace
├── values.yaml     your overrides of the chart defaults
└── obol-app.yaml   release name + the helm revision obol last synced (managed by obol)
```

The namespace and the helm release both default to `<name>-<id>`; `--namespace` and `--release` override them. Namespaces the stack owns (`kube-*`, `default`, `traefik`, `x402`, `llm`, `erpc`, `monitoring`, `obol-frontend`, `reloader`, `hermes-*`, `ethereum-*`, `aztec-*`) are refused.

## Install

```bash
obol app install obol/dv-pod@0.24.0 --name dv-pod --id hoodi --set network=hoodi --values my-dv.yaml
obol app install bitnami/redis --set auth.enabled=false
obol app install oci://registry-1.docker.io/bitnamicharts/redis --name cache --id prod
```

**Chart references.** For `repo/chart[@version]`, obol looks the repo name up in this order:

1. Your local helm repos (`obol helm repo list`).
2. The built-in alias `obol`, which points to Obol's chart repo `https://obolnetwork.github.io/helm-charts`.
3. ArtifactHub. ArtifactHub lists Obol's repo as `charon`, so `charon/dv-pod` works as well.

A direct `https://…/chart-x.y.z.tgz` URL or an `oci://` reference works too.

**Release name.** Pass `--release <name>` to choose the release name. It is stored in `helmfile.yaml` and `obol-app.yaml`, so `sync` and `delete` keep using it.

- New installs default to `<name>-<id>`, for example `dv-pod-hoodi` in namespace `dv-pod-hoodi`.
- Deployments installed before v0.19 keep their release, `<name>` or whatever their `helmfile.yaml` says. Renaming would orphan the running release.
- `install --force` over an existing deployment keeps that deployment's release name.

**values.yaml holds only your overrides.** It contains:

- whatever you passed with `--values` and `--set`;
- a comment header with the command that prints all of the chart's defaults (`obol helm show values …`).

Chart defaults are read from the chart on every sync, so a chart upgrade brings its new defaults, image tags included. Charts that ship `values-examples/obol-stack.yaml` (dv-pod does) start from that file, with its comments.

Comments, key order and quoting survive every write. Strings that look like numbers, such as `0x…` addresses and hashes, are always quoted, so helm never reads them as floats.

**Existing helm releases.** Install refuses if a helm release of the same name already exists in the namespace and obol app doesn't manage it. To take one over, pass `--adopt` with its namespace and release: values.yaml is then seeded from the release's current values (`helm get values`), and its current revision becomes the sync baseline.

```bash
# a dv-pod installed with: obol helm upgrade --install my-dv-pod obol/dv-pod -n dv-pod ...
obol app install obol/dv-pod@<its-chart-version> --name dv-pod --id hoodi \
  --namespace dv-pod --release my-dv-pod --adopt
```

Use the chart version the release runs now (`obol helm list -n dv-pod`), then upgrade separately (see below).

## Sync

```bash
obol app sync dv-pod/hoodi
obol app sync dv-pod/hoodi --set charon.nickname=my-node   # persisted into values.yaml
```

After every successful sync, obol records the helm revision in `obol-app.yaml`. On the next sync it compares the live release with that record:

- **The live revision is newer.** Someone ran `obol helm upgrade` or `rollback` directly, and this sync would revert it. Sync refuses and names the keys that differ between the live release and values.yaml (key names only, never values). Copy the changes into values.yaml (`obol helm get values <release> -n <ns>` shows them) and run sync again, or pass `--force` to overwrite them.
- **The release exists but obol never synced this deployment.** The release was installed outside obol app, so sync refuses unless you pass `--force`.
- **The release is missing.** For example, the cluster was recreated. Sync installs it again.
- **The deployment was installed before v0.19.** It has no sync record, so sync only warns when the live values differ from values.yaml, then records the revision.

The same check runs from `obol upgrade` and the app replay in `obol stack up`. A drifted app is reported there, not reverted.

**values.yaml from before v0.19** is a full copy of the chart's defaults, so it pins that version's image tags. Sync warns when the chart version in `helmfile.yaml` moves away from the deployed one. To slim the file, either:

- keep only the keys you changed (compare with `obol helm show values` for the old version), or
- reinstall with only your overrides: `obol app install <chart>@<version> --name <n> --id <id> --force --values overrides.yaml`.

Sync also warns about unquoted long hex values. Older obol versions wrote them bare, and helm reads them as floats.

**Pods parked in Init.** A StatefulSet never replaces a pod that isn't Ready. So a dv-pod still in Init (for example, waiting for a Launchpad invite or its DKG) would keep the old spec after you change its values.

After a successful sync, obol waits up to 60 s for the normal rollout. Then it deletes any pod that is still in Init and on the old revision, so the StatefulSet recreates it from the new spec. Its volumes and Secrets are kept.

A pod whose containers have started is never deleted, even if it isn't Ready: charon or the validator client may be running.

## Upgrade the chart

```bash
obol app upgrade dv-pod/hoodi --version 0.24.0
```

`obol app upgrade`:

1. Checks that the repo has that chart version.
2. Sets `version:` in the app's `helmfile.yaml`, keeping its comments.
3. Runs `obol app sync` with the usual drift check. If the sync is refused because the release was changed outside obol app, `helmfile.yaml` goes back to the old version; `--force` overrides the check.

values.yaml is not changed. Read the new version's notes and its `values-examples/obol-stack.yaml` (if it ships one) for new settings, and add them to values.yaml yourself.

A deployment from before v0.19 whose values.yaml is a full copy of the old chart's defaults gets a warning: those defaults, image tags included, would stay pinned. Slim it first with `obol app install <chart>@<new-version> --name <n> --id <id> --force --values <your-overrides.yaml>`. That keeps the release, namespace and sync record, and the next `obol app sync` deploys the new version.

Doing it by hand comes to the same thing: edit `version: "<new>"` under `releases:` in `helmfile.yaml`, then run `obol app sync <app>/<id>`.

A chart installed from a versioned `.tgz` URL can't be upgraded this way; reinstall it from the new URL with `--force`.

## Delete

```bash
obol app delete redis/prod                         # namespace + config (confirm, or --force)
obol app delete dv-pod/hoodi                       # data present: removes only the helm release
obol app delete dv-pod/hoodi --keep-data           # only the helm release, always
obol app delete dv-pod/hoodi --delete-data         # everything; type the app name to confirm
obol app delete dv-pod/hoodi --delete-data --yes   # everything, no terminal needed
```

Before deleting anything, obol checks the namespace for PersistentVolumeClaims and for Secrets that look like key material. It reads only names, types and data keys. For a dv-pod, that means:

- the `charon-data-*` and `validator-data-*` PVCs (cluster lock, key shares, slashing protection);
- the ENR key Secrets (`<fullname>-enr-key`, `<fullname>-enr-provided-key`, the legacy `charon-enr-private-key`), where `<fullname>` is the release name, plus `-dv-pod` unless the release name already contains it;
- validator keystore Secrets.

If it finds any of these, or can't check, it removes only the helm release. The namespace, PVCs, Secrets and config directory stay, and `obol app sync` redeploys onto them. dv-pod's PVCs use the `Retain` retention policy, so they survive the release uninstall.

`--force` only skips the yes/no prompt. It never deletes data.

A namespace that also holds other helm releases (an adopted release in a namespace you made by hand) is never deleted as a whole: only this app's release is removed, and `--delete-data` refuses and tells you which PVCs to delete by name.

## Secrets

`obol app secret` writes a Secret without its values going through your shell, a helm value or the terminal:

```bash
obol app secret set dv-pod/hoodi obol-monitoring --from-file token=./monitoring-token
obol app secret set myapp/x api-keys --from-env-file ./keys.env
obol app secret list dv-pod/hoodi
```

- **Reading.** obol reads the files itself and passes the Secret to kubectl on stdin, through server-side apply with field manager `obol-app`. Values never appear in arguments, output or errors; errors in an env file name only the line number.
- **Updates.** Keys this command set before are replaced. Keys added by other tools stay.
- **Namespace.** The app's namespace is created if it doesn't exist yet, so you can create the Secret before the first sync.
- **Listing.** `list` shows names, types and key names, and flags key material.

## dv-pods in the stack

Install a dv-pod with `obol app` and change it only through values.yaml plus `obol app sync`. Always pass `--set network=<network>`: the chart's stack example defaults to `network: mainnet`.

```bash
obol app install obol/dv-pod@0.24.0 --name dv-pod --id hoodi \
  --set network=hoodi --set charon.operatorAddress=0xYOUR_OPERATOR_ADDRESS
obol app sync dv-pod/hoodi
```

Don't also run `obol helm upgrade` on a deployment that obol app manages. The next sync refuses because the release drifted, and with `--force` it reverts the change. A dv-pod already installed with `obol helm upgrade --install` either stays on helm, or moves to obol app with `obol app install … --namespace <its-namespace> --release <its-release> --adopt`.

The chart's `values-examples/obol-stack.yaml` is the starting point. It turns on the ServiceMonitor (Prometheus scrape) and `statusReaders`, which let Hermes read the DKG status and ENR ConfigMaps.

**Identity first (dv-pod 0.24.0 and later).** To hand out your ENR before a cluster definition exists, install with `--set mode=enr` instead of parking the node on a placeholder Launchpad invite. In this mode:

- the chart creates and publishes the ENR, runs no pod, never polls the Obol API, and writes phase `WaitingForDefinition`;
- read the ENR with `obol kubectl get cm <fullname>-enr -n <namespace> -o jsonpath='{.data.enr}'`;
- the next step (below) reuses the same ENR Secret, so the ENR you shared stays valid.

```bash
obol app install obol/dv-pod@0.24.0 --name dv-pod --id hoodi --set network=hoodi --set mode=enr
obol app sync dv-pod/hoodi
```

Chart 0.23.x has no `mode: enr`. There, waiting for a definition means `mode: launchpad` with a placeholder invite, which polls the Obol API.

**Moving to the definition (direct DKG) step:**

1. Edit values.yaml: set `mode: definition` and `dkg.definition.configHash: "0x…"`. obol keeps the quotes.
2. Run `obol app sync`.

**Central monitoring:**

1. Create the token Secret: `obol app secret set dv-pod/hoodi obol-monitoring --from-file token=<file>`. The key must be `token`, the chart's default `centralMonitoring.existingSecretKey`.
2. Set `centralMonitoring.enabled: true` and `centralMonitoring.existingSecret: obol-monitoring` in values.yaml.
3. Run `obol app sync`.

**No sale hints.** After a sync, `obol app sync` suggests `obol sell http` commands for an app's Services. It never suggests them for:

- dv-pod, charon, charon-relay, remote-signer or any other validator or signer chart;
- any Service exposing a validator, signer or node-internal port: 3600 (charon validator API, which lets a caller make the DV sign), 3610, 3620, 3630, 3640, 5051, 5052, 5054, 5062, 7500, 8545, 8546, 8551, 9000, 9001 or 30303.

### Peer connectivity: relays only by default

By default (`charon.directConnectionEnabled: true`) a dv-pod advertises the in-cluster name `<pod>.<fullname>.<namespace>.svc`. The stack also publishes no p2p port: k3d maps only 80, 8080, 443 and 8443 to the host. As a result:

- dv-pods in the same stack reach each other directly, through cluster DNS;
- operators outside this machine reach a stack dv-pod only through charon relays (`charon.p2pRelays`, which defaults to Obol's relays). The DKG and validator duties work over relays, with an extra hop of latency, and depend on the relays being up.

The chart can accept direct connections from remote peers, but it needs host-level changes that obol doesn't make for you:

```yaml
charon:
  directConnectionEnabled: false      # stop advertising the in-cluster name
  p2pExternalIp: "203.0.113.10"       # this host's public IP (or p2pExternalHostname)
  p2pTcpAddress: "0.0.0.0:31610"      # listen on the node port
p2pService:
  enabled: true
  type: NodePort
  nodePorts:
    tcp: 31610
service:
  ports:
    p2pTcp:
      targetPort: 31610
```

Then:

1. **Publish the node port on the host.** On the k3d backend the node port lives inside the k3d node container, so the host needs a port mapping. Add one with `k3d cluster edit <cluster> --port-add 31610:31610@loadbalancer`, which recreates k3d's load balancer. obol doesn't record this mapping: `obol stack init --force` or a cluster recreate drops it. The k3s backend binds node ports on the host directly.
2. **Forward TCP 31610 to this host** on the firewall or router, and make sure `p2pExternalIp` is the address peers can reach.
3. **Expect in-stack peers to switch paths.** With `directConnectionEnabled: false`, other dv-pods in the same stack also use the external address (hairpin NAT) or relays.

A built-in feature would need to:

- record the port mapping in the stack config and re-apply it on `stack up`;
- choose a free node port per dv-pod;
- detect or ask for the public address;
- check that the port is reachable before advertising it.
