{
  description = "obol-stack -- A Kubernetes-based Ethereum stack for running decentralised applications. ";

  inputs = {
    flake-parts.url = "github:hercules-ci/flake-parts";
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    devshell.url = "github:numtide/devshell";
  };

  outputs = inputs @ {flake-parts, ...}:
    flake-parts.lib.mkFlake {inherit inputs;} {
      imports = [inputs.devshell.flakeModule];

      systems = ["x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin"];

      perSystem = {pkgs, ...}: {
        devshells.default = {
          packages = with pkgs; [
            just

            # bash
            shellcheck

            # golang
            go
            go-tools
            gopls
            gotools

            # nix
            alejandra
          ];
        };
      };
    };
}
