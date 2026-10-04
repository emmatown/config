{
  config,
  lib,
  pkgs,
  unstablePkgs,
  ...
}:
{
  options.profiles.javascript.enable = lib.mkOption {
    type = lib.types.bool;
    default = false;
    description = "Enable the JavaScript tools and shell integration.";
  };

  config = lib.mkIf config.profiles.javascript.enable {
    home.packages = with pkgs; [
      (rustPlatform.buildRustPackage {
        pname = "pyn";
        version = "0.6.1";

        src = fetchFromGitHub {
          owner = "Thinkmill";
          repo = "pyn";
          rev = "79ada70c194d2f77c034afc6eabd0f0bbc9312e0";
          hash = "sha256-UQnJKKvurfDli+rhxiJELzpvPl0G/iaxIwLEeg3S6BY=";
        };
        doCheck = false;
        cargoHash = "sha256-3wPfzmAhoOhaS9rtaCUfLRDaHMpuEuIR2P33eXyXL4Y=";
      })
      unstablePkgs.fnm
      unstablePkgs.pnpm
    ];
    programs.zsh = {
      initContent = ''
        eval "$(fnm env --use-on-cd --shell zsh)"
      '';
      shellAliases = {
        r = "pyn";
      };
    };
  };
}
