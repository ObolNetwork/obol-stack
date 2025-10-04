# `obolup`

`obolup` is a script for launching (and updating) the Obol Stack. It is in early Alpha, and subject to breaking changes. It is not yet recommended as a production runtime. 

## Usage

To start a light-client, mainnet node, simply run `./obolup` from this directory.

To start a Hoodi testnet instance of the Obol Stack, run `./obolup --network hoodi`.

To start the Obol Stack without access to your host machine's web browser run `./obolup --headless`. 

```text
Usage: ./obolup [--help] [--network <network>] [--mode <mode>] [--headless] [--clean]

Options:
  --help                            Display this help message
  --network <network>               Specify the network (default: mainnet) [mainnet, hoodi, sepolia]
  --mode <mode>                     Specify the type of sync mode (default: light) [light, full]
  --headless                        Skips exposing the stack to the host operating system
  --clean                           Deletes and recreates the Obol Stack
```

To shut down a running instance, run `./oboldown` from this directory. This clears all stack data.

### Troubleshooting

Adding the `--clean` flag will start your cluster fresh, and may fix issues during alpha usage or version upgrades. 

> [!WARNING]
> This will clear the stored data of your stack.

## Supported Architectures

| Operating System | Architecture | Supported |
|------------------|--------------|-----------|
| Linux            | amd64        | 🚧        |
| Linux            | arm64        | 🚧        |
| macOS            | amd64        | ❌        |
| macOS            | arm64        | 🚧        |
| Windows          | amd64        | ❌        |
| Windows          | arm64        | ❌        |
| FreeBSD          | amd64        | ❌        |
| FreeBSD          | arm64        | ❌        |
