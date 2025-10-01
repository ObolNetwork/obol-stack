{
  description = "Description for the project";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    devshell.url = "github:numtide/devshell";
    obol-cli.url = "git+ssh://git@github.com/ObolNetwork/obol-cli";
  };

  outputs = inputs @ {flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      imports = [inputs.devshell.flakeModule];

      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin"];

      perSystem = {pkgs, ...}: {
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

              # nix
              alejandra
            ]
            ++ [inputs.obol-cli.packages.${pkgs.system}.default];
        };
      };
    };
}
