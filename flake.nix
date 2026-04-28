{
  description = "Home Manager configuration of emma";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts.url = "github:hercules-ci/flake-parts";
    nix-vscode-extensions.url = "github:nix-community/nix-vscode-extensions";
  };

  outputs =
    inputs@{
      flake-parts,
      nixpkgs,
      ...
    }:
    let
      mkHome =
        {
          username,
          system,
        }:
        inputs.home-manager.lib.homeManagerConfiguration {
          pkgs = import nixpkgs {
            inherit system;
            config.allowUnfree = true;
            overlays = [ inputs.nix-vscode-extensions.overlays.default ];
          };
          modules = [
            ./home.nix
            {
              home = {
                inherit username;
                homeDirectory =
                  if builtins.match ".*-darwin" system != null then "/Users/${username}" else "/home/${username}";
              };
            }
          ];
          extraSpecialArgs = {
            inherit inputs;
          };
        };
    in
    flake-parts.lib.mkFlake { inherit inputs; } (
      _top@{ ... }:
      {
        imports = [
          inputs.home-manager.flakeModules.home-manager
        ];

        flake = {
          homeModules.default = ./home.nix;

          homeConfigurations = {
            emma-darwin = mkHome {
              username = "emma";
              system = "aarch64-darwin";
            };

            emma-linux = mkHome {
              username = "emma";
              system = "x86_64-linux";
            };
            exedev = mkHome {
              username = "exedev";
              system = "x86_64-linux";
            };
          };
        };

        systems = [
          "aarch64-darwin"
          "x86_64-linux"
        ];
      }
    );
}
