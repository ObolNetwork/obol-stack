<div align="center">
  <img src="https://obol.org/obolnetwork.png" alt="Obol banner" />

&nbsp;

<h1>The Obol Stack: Decentralised Applications For Ethereum</h1>

</div>

## Overview

The Obol Stack is a framework to make it easier to distribute decentralised
applications (dApps), and easier to install and run them locally. The stack is
built on [Kubernetes](https://kubernetes.io), with [Helm](https://helm.sh/) as a
package management system.

![Demo of the Stack Front End](./assets/frontend.gif)

## Getting Started

> [!IMPORTANT]
> The Obol Stack is alpha software. It is not complete, and it may not be
> working smoothly. If you encounter an issue that does not appear to be
> documented, please open a
> [GitHub issue](http://github.com/obolNetwork/obol-stack/issues) if an
> appropriate one is not already present.

### Prerequisites

The Obol Stack requires [Docker](https://www.docker.com/) to run a local Kubernetes cluster. Install Docker:

- **Linux**: Follow the [Docker Engine installation guide](https://docs.docker.com/engine/install/)
- **macOS/Windows**: Install [Docker Desktop](https://docs.docker.com/desktop/)

### Installation

The easiest way to install the Obol Stack is using the `obolup` bootstrap installer.

Run the installer with:

```bash
curl -fsSL https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/obolup.sh | bash
```

**What the installer does:**

1. Verifies Docker is running
2. Installs the `obol` CLI binary to `~/.local/bin/obol`
3. Installs required dependencies (kubectl, helm, k3d, helmfile, k9s) to `~/.local/bin/`
4. Adds `obol.stack` to your `/etc/hosts` file (requires sudo) to enable local domain access
5. Prompts you to add `~/.local/bin` to your PATH by updating your shell profile
6. Prompts you to start the cluster and open the Obol application in your browser

**PATH Configuration:**

The installer will detect your shell (bash/zsh) and ask if you want to automatically add `~/.local/bin` to your PATH. If you choose automatic configuration, it will add this line to your shell profile (`~/.bashrc` or `~/.zshrc`):

```bash
export PATH="$HOME/.local/bin:$PATH"
```

After installation, reload your shell configuration:

```bash
# For bash
source ~/.bashrc

# For zsh
source ~/.zshrc
```

**Manual PATH Configuration:**

If you prefer to configure PATH manually, add this line to your shell profile:

```bash
# Add to ~/.bashrc (bash) or ~/.zshrc (zsh)
export PATH="$HOME/.local/bin:$PATH"
```

Then reload your shell or start a new terminal session.

**Using obol without PATH:**

If you haven't added `~/.local/bin` to your PATH, you can always run commands directly:

```bash
~/.local/bin/obol version
~/.local/bin/obol stack init
```

**Verify the installation:**

```bash
obol version
```

### Quick Start

Once installed, you can start your local Ethereum stack with three commands:

```bash
# Initialize the stack configuration
obol stack init

# Start the Kubernetes cluster
obol stack up

# View cluster (opens interactive terminal UI)
obol k9s
```

The stack will create a local Kubernetes cluster and the Obol Stack frontend will be available at:

- **Obol Stack**: http://obol.stack (or http://localhost if using port 80)

### Managing the Stack

**Start the stack:**
```bash
obol stack up
```

**Stop the stack:**
```bash
obol stack down
```

**View cluster (interactive UI):**
```bash
obol k9s
```

**View cluster logs:**
```bash
obol kubectl logs -n <namespace> <pod-name>
```

**Remove everything (including data):**
```bash
obol stack purge -f
```

> [!WARNING]
> The `purge` command permanently deletes all cluster data and configuration. The `-f` flag is required to remove persistent volume claims (PVCs) owned by root. Use with caution.

### Working with Kubernetes

The `obol` CLI includes convenient wrappers for common Kubernetes tools. These automatically use the correct cluster configuration:

```bash
# Kubectl (Kubernetes CLI)
obol kubectl get pods --all-namespaces

# Helm (Kubernetes package manager)
obol helm list --all-namespaces

# K9s (interactive cluster manager)
obol k9s

# Helmfile (declarative Helm releases)
obol helmfile list
```

### Troubleshooting

#### Port 80 Already in Use

The Obol Stack is configured to run on ports 80 and 443 by default. If you have another service using these ports, the cluster may fail to start.

**To fix this:**

1. Edit the k3d configuration file:
   ```bash
   $EDITOR ~/.config/obol/k3d.yaml
   ```

2. Find the ports section that looks like this:
   ```yaml
   ports:
     - port: 80:80
       nodeFilters:
         - loadbalancer
     - port: 8080:80
       nodeFilters:
         - loadbalancer
     - port: 443:443
       nodeFilters:
         - loadbalancer
     - port: 8443:443
       nodeFilters:
         - loadbalancer
   ```

3. Remove the `80:80` and `443:443` entries (keep the 8080 and 8443 entries):
   ```yaml
   ports:
     - port: 8080:80
       nodeFilters:
         - loadbalancer
     - port: 8443:443
       nodeFilters:
         - loadbalancer
   ```

4. Restart the cluster:
   ```bash
   obol stack down
   obol stack up
   ```

After this change, access the Obol Stack frontend using port 8080:
- **Obol Stack**: http://obol.stack:8080 (or http://localhost:8080)

> [!TIP]
> If ports 8080 or 8443 are also in use, you can change them to any available port. For example, change `8080:80` to `9090:80` and `8443:443` to `9443:443`. Then access the application at http://obol.stack:9090 or http://localhost:9090

### Where Files Are Stored

The Obol Stack follows the [XDG Base Directory](https://specifications.freedesktop.org/basedir-spec/basedir-spec-latest.html) specification:

- **Configuration**: `~/.config/obol/` - Cluster config, kubeconfig, application manifests
- **Data**: `~/.local/share/obol/` - Persistent volumes and database storage
- **Binaries**: `~/.local/bin/` - The `obol` CLI and dependencies
- **Logs**: `~/.local/state/obol/` - Structured logs for debugging

#### Uninstalling Obol Stack

To completely remove the Obol Stack from your system:

**1. Stop and remove the cluster:**
```bash
~/.local/bin/obol stack purge -f
```

> [!NOTE]
> The `-f` flag is required to remove persistent volume claims (PVCs) that are owned by root. Without this flag, data volumes will remain on your system.

**2. Remove Obol binaries:**
```bash
rm -f ~/.local/bin/obol \
      ~/.local/bin/kubectl \
      ~/.local/bin/helm \
      ~/.local/bin/k3d \
      ~/.local/bin/helmfile \
      ~/.local/bin/k9s \
      ~/.local/bin/obolup.sh
```

**3. Remove Obol directories:**
```bash
rm -rf ~/.config/obol \
       ~/.local/share/obol \
       ~/.local/state/obol
```

> [!NOTE]
> This process removes Obol binaries from `~/.local/bin/`. If you installed kubectl, helm, k3d, helmfile, or k9s separately before installing Obol, make sure not to delete those binaries. The PATH configuration in your shell profile is left unchanged.

### Updating the Stack

To update to the latest version, simply run the installer again:

```bash
curl -fsSL https://raw.githubusercontent.com/ObolNetwork/obol-stack/main/obolup.sh | bash
```

The installer will detect your existing installation and upgrade it safely.

### Development Mode

If you're contributing to the Obol Stack or want to run it from source, you can use development mode.

**Setting up development mode:**

1. Clone the repository:
   ```bash
   git clone https://github.com/ObolNetwork/obol-stack.git
   cd obol-stack
   ```

2. Run the installer in development mode:
   ```bash
   OBOL_DEVELOPMENT=true ./obolup.sh
   ```

**What development mode does:**

- Uses a local `.workspace/` directory instead of XDG directories (`~/.config/obol`, etc.)
- Installs a wrapper script that runs the `obol` CLI using `go run` (no compilation needed)
- Code changes are immediately reflected when you run `obol` commands
- All cluster data, configuration, and logs are stored in `.workspace/`

**Development workspace structure:**

```
.workspace/
├── bin/                         # obol wrapper script and dependencies
├── config/                      # Cluster configuration
│   ├── k3d.yaml
│   ├── .cluster-id
│   ├── kubeconfig.yaml
│   └── applications/
├── data/                        # Persistent volumes
└── state/                       # Logs
    └── {cluster-id}/
        └── 2025-01-15.log
```

**Making code changes:**

Simply edit the Go source files and run `obol` commands as normal. The wrapper script automatically compiles and runs your changes:

```bash
# Edit source files
$EDITOR cmd/obol/main.go

# Run immediately - no build step needed
obol stack up
```

**Switching back to production mode:**

First, purge the development cluster to remove root-owned PVCs, then remove the `.workspace/` directory and reinstall:

```bash
obol stack purge -f
rm -rf .workspace
./obolup.sh
```

> [!NOTE]
> The `obol stack purge -f` command is necessary to remove persistent volume claims (PVCs) owned by root. Without the `-f` flag, these files will remain and may cause issues.

<!-- ## Stack Overview -->

<!-- The default installation of the Stack configures an Ethereum L1 light client -->
<!-- (using [Helios](https://github.com/a16z/helios)) and when `--mode=full` is -->
<!-- passed, the stack syncs an L1 full node. Both sit behind a specialised Ethereum -->
<!-- load balancer called [eRPC](https://erpc.cloud/). The stack aims to provide a -->
<!-- high quality L1 RPC for all dApps installed on the stack. The default address -->
<!-- for this RPC is: -->

<!-- ```bash -->
<!-- # Obol Stack L1 JSON-RPC for Obol Apps running within the stack -->
<!-- http://rpc.l1.cluster.svc.local/rpc/mainnet -->
<!-- http://rpc.l1.cluster.svc.local/rpc/hoodi -->

<!-- # Obol Stack L1 Beacon Node API for Obol Apps in the stack that communicate with Ethereum's consensus layer -->
<!-- http://l1-full-node-beacon.l1.cluster.svc.local:5052 -->

<!-- # Obol Stack L1 JSON-RPC accessible by the host OS -->
<!-- http://obol.stack/rpc/mainnnet -->
<!-- http://obol.stack/rpc/hoodi -->
<!-- ``` -->

<!-- ### `host` mode -->

<!-- By default, the Obol Stack configures itself to be accessible to dApps in your -->
<!-- web browser, such as wallets and dApps. The stack configures itself on custom -->
<!-- domain; https://obol.stack/ This behaviour can be disabled by running the stack -->
<!-- in `--headless` mode. -->

<!-- > [!INFO] -->
<!-- > When accessing the Obol Stack from your host OS, your browser may warn you -->
<!-- > about self-signed HTTPS certificates. This is unavoidable when using custom -->
<!-- > local web domains. You should click "Accept the risk and continue" to access -->
<!-- > the stack web page. -->

<!-- ### Installing an Obol App (Helm Chart) -->

<!-- Here's an example of adding on a popular Ethereum sidecar called contributoor, -->
<!-- built by the EthPandaOps team, which streams data from your full node to their -->
<!-- backend for analysis and visualisation. -->

<!-- ```bash -->
<!-- obol install ethereum/contributooor -->
<!-- ``` -->

<!-- ### Adding another Obol App Store -->

<!-- The Obol Stack is built on Helm, so you can add your own Helm Chart repository -->
<!-- easily. -->

<!-- ```bash -->
<!-- # Add a repository of Helm Charts -->
<!-- obol repo add ithaca https://github.com/ithacaxyz/obol-charts -->
<!-- # Install a chart from the new 'App Store' -->
<!-- obol install ithaca/op-reth -->
<!-- ``` -->

<!-- ### Custom deployments -->

<!-- Each Obol App has a `values.yaml` file with default values. You can customize -->
<!-- these values by creating your own values file and passing it to the install -->
<!-- command: -->

<!-- ```bash -->
<!-- obol install <app-store-name>/<chart-name> --values custom-values.yaml -->
<!-- ``` -->

<!-- ### Using advanced tooling -->

<!-- The `obol` CLI is intended to be a simple command-line user interface to -->
<!-- simplify the use of the Obol Stack for non-developers, it is a work in progress, -->
<!-- and does not cover many advanced use cases that Kubernetes and Helm can offer. -->
<!-- If you are an experienced Kubernetes user, `obolup` also installs -->
<!-- [`kubectl`](https://kubernetes.io/docs/reference/kubectl/) and -->
<!-- [`helm`](https://helm.sh/docs/helm/helm/), such that you can manage your stack -->
<!-- with the tooling you are used to. -->

<!-- If you encounter node management requirements that an end-user might need but -->
<!-- cannot achieve with the Obol CLI, instead needing to use `kubectl` or `helm`, -->
<!-- consider opening a feature request issue on the -->
<!-- [obol-cli](https://github.com/ObolNetwork/obol-cli/issues) repo. -->

## Project Status

This project is currently in alpha, and should not be used in production.

The stack aims to support all popular Kubernetes backends and all Ethereum
client types, with a developer experience designed to be useful for local app
development, through to production deployment and management.

## Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md) for details.

## License

This project is licensed under the [Apache License 2.0](LICENSE).
