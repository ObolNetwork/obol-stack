{
  lib,
  config,
  ...
}: {
  applications.l1 = let
    global = {
      checkpointSync = {
        enabled = true;
        networks = {
          mainnet = "https://mainnet-checkpoint-sync.attestant.io";
          sepolia = "https://checkpoint-sync.sepolia.ethpandaops.io";
          holesky = "https://checkpoint-sync.holesky.ethpandaops.io";
          hoodi = "https://checkpoint-sync.hoodi.ethpandaops.io";
        };
      };
    };
  in {
    namespace = "l1";
    createNamespace = true;
    helm.releases.l1 = {
      chart = lib.helm.downloadHelmChart {
        repo = "https://ethpandaops.github.io/ethereum-helm-charts/";
        chart = "ethereum-node";
        version = "0.2.4";
        chartHash = "sha256-WcqjsPuivBAbEMFF9Qb8KUPRBBfWy7hhErutrlxPfx0=";
      };
      values = {
        geth = {
          enabled = true;
          nameOverride = "execution";
          httpPort = 8545;
          wsPort = 8546;
          p2pPort = 30303;
          extraArgs = [
            "--${config.obol-stack.settings.network}"
          ];
        };
        lighthouse = {
          enabled = true;
          nameOverride = "beacon";
          httpPort = 5052;
          p2pPort = 9000;
          checkpointSync = {
            enabled = global.checkpointSync.enabled;
            url = global.checkpointSync.networks.${config.obol-stack.settings.network};
          };
          extraArgs = [
            "--execution-endpoint=http://l1-execution:8551"
            "--network=${config.obol-stack.settings.network}"
          ];
        };
      };
    };
  };
}
