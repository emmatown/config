{
  description = "Home Manager configuration of emma";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05";
    nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
    llm-agents.url = "github:numtide/llm-agents.nix";
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

          nixosConfigurations.minivm-development = nixpkgs.lib.nixosSystem {
            system = "x86_64-linux";
            modules = [ ./guests/minivm/development.nix ];
          };
          nixosConfigurations.vm-workspace = nixpkgs.lib.nixosSystem {
            system = "x86_64-linux";
            specialArgs.codexPackage = inputs.llm-agents.packages.x86_64-linux.codex;
            modules = [ ./guests/minivm/workspace.nix ];
          };
          nixosConfigurations.vm-gateway = nixpkgs.lib.nixosSystem {
            system = "x86_64-linux";
            modules = [ ./guests/minivm/gateway.nix ];
          };
          nixosConfigurations.vm-inference = nixpkgs.lib.nixosSystem {
            system = "x86_64-linux";
            specialArgs.codexPackage = inputs.llm-agents.packages.x86_64-linux.codex;
            modules = [ ./guests/minivm/inference.nix ];
          };
          nixosModules.minivm = ./modules/minivm;

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

        perSystem =
          {
            pkgs,
            system,
            lib,
            ...
          }:
          {
            devShells.default = pkgs.mkShell {
              packages = [
                pkgs.go
                pkgs.sqlite
              ];
              CGO_ENABLED = "1";
              GOPROXY = "off";
              GOTOOLCHAIN = "local";
            };
            checks = lib.optionalAttrs (system == "x86_64-linux") {
              vm-network-policy = import ./minivm/tests/network-policy.nix {
                inherit pkgs lib;
                gatewayConfig = self.nixosConfigurations.vm-gateway.config;
                hostConfig = self.nixosConfigurations.mini.config;
                inferenceConfig = self.nixosConfigurations.vm-inference.config;
              };
            };
            packages = {
              vm-inference-broker = pkgs.callPackage ./modules/minivm/broker-package.nix { };
              minivm = pkgs.callPackage ./modules/minivm/package.nix { };
              minivm-supervisor = pkgs.callPackage ./modules/minivm/package.nix {
                role = "supervisor";
              };
            }
            // lib.optionalAttrs (system == "x86_64-linux") {
              minivm-development-image = import ./guests/minivm/image.nix {
                inherit pkgs lib;
                config = self.nixosConfigurations.minivm-development.config;
              };
              minivm-firmware = pkgs.OVMF-cloud-hypervisor;
              vm-workspace-image = import ./guests/minivm/image.nix {
                inherit pkgs lib;
                config = self.nixosConfigurations.vm-workspace.config;
                name = "vm-workspace-image";
                diskSize = 65536;
              };
              vm-gateway-image = import ./guests/minivm/image.nix {
                inherit pkgs lib;
                config = self.nixosConfigurations.vm-gateway.config;
                name = "vm-gateway-image";
                diskSize = 8192;
              };
              vm-inference-image = import ./guests/minivm/image.nix {
                inherit pkgs lib;
                config = self.nixosConfigurations.vm-inference.config;
                name = "vm-inference-image";
                diskSize = 8192;
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
