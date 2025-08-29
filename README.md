![Obol Logo](https://obol.tech/obolnetwork.png)

<h1 align="center">The Obol Stack: Decentralised Applications For Ethereum</h1>

## Overview

The Obol Stack is a framework to make it easier to distribute decentralised applications (dApps), and easier to install and run them locally. The stack is built on [Kubernetes](https://kubernetes.io), with [Helm](https://helm.sh/) as a package management system.

<div style="max-width: 100%; text-align: center;">
  <video
    src="https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/assets/frontenddemo.mp4"
    autoplay
    loop
    muted
    playsinline
    style="max-width:100%; height:auto; display:inline-block;"
  >
    Obol Stack Web Interface
  </video>
</div>

## Getting Started

> [!IMPORTANT]
> The Obol Stack is alpha software. It is not complete, and it may not be working smoothly. If you encounter an issue that does not appear to be documented, please open a [github issue](http://github.com/obolNetwork/obol-stack/issues) if an appropriate one is not already present.
>
> See [here](./obolup/README.md#supported-architectures) for the latest on OS and architectures supported.

### Pre-requisites

Running the Obol Stack locally requires a [Docker](https://www.docker.com/) engine. Install Docker for Linux using one of the options [here](https://docs.docker.com/engine/install/). Install Docker Desktop for Other Operating Systems [here](https://docs.docker.com/desktop/).

> [!TIP]
> If you use Docker Desktop, be sure to go to the settings section, resources tab, and allocate most or all of your CPUs and most of your disk space. The stack won't succeed in syncing a local L1 node if there is not enough available disk space.

Once you have Docker installed, the easiest way to bootstrap the stack is to use the `obolup` installer. `obolup` keeps your stack running the latest versions of its software.

> [!IMPORTANT]
> This first method of installation is not yet live, for now you must clone the repo and run `obolup` locally, as described in the second installation option.

```sh
# This mode of installation is not yet live, please use the repo clone approach until this message is removed

# Add the `obolup` program to your path
curl -L https://stack.obol.org | sudo bash

# Reload your terminal, and run `obolup`
obolup
```

You can also clone this repo locally and run:

```sh
# Clone this repo
git clone git@github.com:ObolNetwork/obol-stack.git

# Change to ./obolup subdirectory
cd obol-stack/obolup
sudo chmod u+x ./obolup

# Launch the stack in light client mode
./obolup
```

The complete usage of `obolup` is documented [here](./obolup/README.md).

## Stack Overview

The default installation of the Stack configures an Ethereum L1 light client (using [Helios](https://github.com/a16z/helios)) and when `--mode=full` is passed, the stack syncs an L1 full node. Both sit behind a specialised Ethereum load balancer called [eRPC](https://erpc.cloud/). The stack aims to provide a high quality L1 RPC for all dApps installed on the stack. The default address for this RPC is:

```bash
# Obol Stack L1 JSON-RPC for Obol Apps running within the stack
http://rpc.l1.cluster.svc.local/rpc/mainnet
http://rpc.l1.cluster.svc.local/rpc/hoodi

# Obol Stack L1 Beacon Node API for Obol Apps in the stack that communicate with Ethereum's consensus layer
http://l1-full-node-beacon.l1.cluster.svc.local:5052

# Obol Stack L1 JSON-RPC accessible by the host OS
http://obol.stack/rpc/mainnnet
http://obol.stack/rpc/hoodi
```

### `host` mode

By default, the Obol Stack configures itself to be accessible to dApps in your web browser, such as wallets and dApps. The stack configures itself on custom domain; https://obol.stack/
This behaviour can be disabled by running the stack in `--headless` mode.

> [!INFO]
> When accessing the Obol Stack from your host OS, your browser may warn you about self-signed HTTPS certificates. This is unavoidable when using custom local web domains. You should click "Accept the risk and continue" to access the stack web page.

### Installing an Obol App (Helm Chart)

Here's an example of adding on a popular Ethereum sidecar called contributoor, built by the EthPandaOps team, which streams data from your full node to their backend for analysis and visualisation.

```bash
obol install ethereum/contributooor
```

### Adding another Obol App Store

The Obol Stack is built on Helm, so you can add your own Helm Chart repository easily.

```bash
# Add a repository of Helm Charts
obol repo add ithaca https://github.com/ithacaxyz/obol-charts
# Install a chart from the new 'App Store'
obol install ithaca/op-reth
```

### Custom deployments

Each Obol App has a `values.yaml` file with default values. You can customize these values by creating your own values file and passing it to the install command:

```bash
obol install <app-store-name>/<chart-name> --values custom-values.yaml
```

### Using advanced tooling

The `obol` CLI is intended to be a simple command-line user interface to simplify the use of the Obol Stack for non-developers, it is a work in progress, and does not cover many advanced use cases that Kubernetes and Helm can offer. If you are an experienced Kubernetes user, `obolup` also installs [`kubectl`](https://kubernetes.io/docs/reference/kubectl/) and [`helm`](https://helm.sh/docs/helm/helm/), such that you can manage your stack with the tooling you are used to.

If you encounter node management requirements that an end-user might need but cannot achieve with the Obol CLI, instead needing to use `kubectl` or `helm`, consider opening a feature request issue on the [obol-cli](https://github.com/ObolNetwork/obol-cli/issues) repo.

## Project Status

This project is currently in alpha, and should not be used in production.

The stack aims to support all popular Kubernetes backends and all Ethereum client types, with a developer experience designed to be useful for local app development, through to production deployment and management.

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
