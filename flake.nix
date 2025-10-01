{
  description = "Obol Stack";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    devshell.url = "github:numtide/devshell";
    nixidy.url = "github:arnarg/nixidy";
  };

  outputs = inputs @ {flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      imports = [
        inputs.devshell.flakeModule
        ./cluster
      ];

      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin"];

      perSystem = {
        pkgs,
        inputs',
        ...
      }: {
        devshells.default = {
          packages = with pkgs;
            [
              # obolup
              curl
              kubectl
              podman
              kubernetes-helm
              kind
              k9s

              foundry
              rsync

              # nix
              alejandra
            ]
            ++ [
              inputs'.nixidy.packages.default
            ];
        };
      };
    };
}
