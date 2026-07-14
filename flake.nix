{
  description = "Home Manager configuration of emma";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    flake-parts.url = "github:hercules-ci/flake-parts";
    nix-darwin.url = "github:nix-darwin/nix-darwin/nix-darwin-26.05";
    nix-darwin.inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    inputs@{
      self,
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
          };
          modules = [
            ./home.nix
            {
              home.username = username;
            }
          ];
          extraSpecialArgs = {
            inherit inputs;
          };
        };

      installer = nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        modules = [ ./nixos/installer.nix ];
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

          packages.x86_64-linux.installer-iso = installer.config.system.build.isoImage;

          darwinConfigurations.mbp = inputs.nix-darwin.lib.darwinSystem {
            modules = [
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
                  };
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
