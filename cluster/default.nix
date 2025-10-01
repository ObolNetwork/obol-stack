{inputs, ...}: let
  modules = [
    ./modules/l1.nix
    ./modules/nixidy.nix
  ];
in {
  # We define this to have access to the config in the repl
  flake = let
    cluster =
      (inputs.nixidy.lib.mkEnv {
        inherit modules;
        pkgs = inputs.nixpkgs.legacyPackages."x86_64-linux";
      }).config.applications;
  in {
    inherit cluster;
  };

  perSystem = {pkgs, ...}: let
    cluster = inputs.nixidy.lib.mkEnv {
      inherit pkgs modules;
    };
  in {
    # TODO It would be nice to define these as their own transposition
    packages = rec {
      clusterActivation = cluster.activationPackage;
      clusterBootstrap = cluster.bootstrapPackage;
      clusterDeclarative = cluster.declarativePackage;
      clusterEnvironment = cluster.environmentPackage;

      default = clusterActivation;
    };
  };
}
