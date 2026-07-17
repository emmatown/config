{
  description = "Home Manager configuration of emma";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05";
    nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager/release-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts.url = "github:hercules-ci/flake-parts";
    nix-darwin = {
      url = "github:nix-darwin/nix-darwin/nix-darwin-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    nix-rosetta-builder = {
      url = "github:cpick/nix-rosetta-builder";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    disko = {
      url = "github:nix-community/disko";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    lanzaboote = {
      url = "github:nix-community/lanzaboote/v1.1.0";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    inputs@{
      self,
      flake-parts,
      nixpkgs,
      nix-rosetta-builder,
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
          };
          modules = [
            ./home.nix
            {
              home.username = username;
            }
          ];
          extraSpecialArgs = {
            inherit inputs;
            unstablePkgs = inputs.nixpkgs-unstable.legacyPackages.${system};
          };
        };

      installer = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [ ./hosts/installer.nix ];
      };
    in
    flake-parts.lib.mkFlake { inherit inputs; } (
      _top@{ ... }:
      {
        imports = [
          inputs.home-manager.flakeModules.home-manager
        ];

        flake = {
          nixosConfigurations.installer = installer;
          nixosConfigurations.mini = nixpkgs.lib.nixosSystem {
            system = "x86_64-linux";
            specialArgs = { inherit inputs; };
            modules = [ ./hosts/mini ];
          };

          darwinConfigurations.mbp = inputs.nix-darwin.lib.darwinSystem {
            modules = [
              nix-rosetta-builder.darwinModules.default
              (
                { ... }:
                {
                  system.configurationRevision = self.rev or self.dirtyRev or null;
                  system.stateVersion = 6;
                  nixpkgs.hostPlatform = "aarch64-darwin";
                  nix = {
                    settings.experimental-features = [
                      "nix-command"
                      "flakes"
                    ];
                    # for bootstrapping nix-rosetta-builder
                    # linux-builder.enable = true;
                  };
                  nix-rosetta-builder.onDemand = true;
                }
              )
            ];
          };

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
