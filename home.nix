{
  config,
  lib,
  pkgs,
  ...
}:

{
  imports = [
    ./modules/home/shell.nix
    ./modules/home/vcs.nix
    ./modules/home/nix.nix
    ./modules/home/js.nix
    ./modules/home/secure-enclave-ssh.nix
  ];

  home = {
    homeDirectory = lib.mkDefault (
      if pkgs.stdenv.isDarwin then "/Users/${config.home.username}" else "/home/${config.home.username}"
    );
    stateVersion = "26.05";
    packages = with pkgs; [
      ripgrep
      rage
    ];

    file = { };

    sessionVariables = lib.optionalAttrs pkgs.stdenv.isDarwin {
      EDITOR = lib.mkDefault "code --wait";
    };
  };

  programs.home-manager.enable = true;
}
