# Obol Stack

A repository for distributing and running decentralised applications, powered by Kubernetes and Helm.

## Overview

The Obol Stack is a framework to make it easier to distribute dApps, and easier to install them. The stack is built on Kubernetes, with Helm as a package management system.

## Getting Started

The easiest way to get started is to use download the `obolup` installer. `obolup` keeps your stack running the latest versions of its software.

```sh
# Add the `obolup` program to your path
curl -L https://stack.obol.org | sudo bash

# Reload your terminal, and run `obolup`
obolup
```

You can also clone this repo locally and run:
```sh
git clone git@github.com:ObolNetwork/obol-stack.git
cd obol-stack
sudo chmod u+x ./obolup/obolup
./obolup/obolup
```

### Installing an Obol App (Helm Chart)

Here's an example of adding on a popular Ethereum sidecar called contributoor, built by the EthPandaOps team, which streams data from your full node to their backend for analysis and visualisation.

```bash
obol install ethereum/contributooor
```

### Adding another Obol App Store

The Obol Stack is built on Helm, you can add your own Helm Chart repository easily.

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

## Project Status

This project is currently in alpha, and should not be used in production.

The stack aims to support all popular Kubernetes backends, with a developer experience designed to be useful for local app development, through to production deployment and management.

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
