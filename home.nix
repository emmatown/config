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

    # This value determines the Home Manager release that your configuration is
    # compatible with. This helps avoid breakage when a new Home Manager release
    # introduces backwards incompatible changes.
    #
    # You should not change this value, even if you update Home Manager. If you do
    # want to update the value, then make sure to first check the Home Manager
    # release notes.
    stateVersion = "26.05"; # Please read the comment before changing.
    packages = with pkgs; [
      ripgrep
      rage
      terraform
    ];

    file = { };

    sessionVariables = {
      EDITOR = "code --wait";
    }
    // lib.optionalAttrs pkgs.stdenv.isDarwin {
      SSL_CERT_FILE = "/etc/ssl/certs/ca-certificates.crt";
      NODE_OPTIONS = "--use-openssl-ca";
    };
    sessionPath = [
      "$HOME/.nix-profile/bin"
    ];
  };

  programs.home-manager.enable = true;
}
