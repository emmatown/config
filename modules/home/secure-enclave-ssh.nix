{ lib, pkgs, ... }:

{
  config = lib.mkIf pkgs.stdenv.isDarwin {
    home.packages = [
      pkgs.yubikey-manager
      (pkgs.writeShellScriptBin "yubikey-ssh" ''
        exec ${pkgs.openssh}/bin/ssh -o SecurityKeyProvider=internal -o IdentityAgent=none "$@"
      '')
      (pkgs.writeShellScriptBin "yubikey-ssh-keygen" ''
        exec ${pkgs.openssh}/bin/ssh-keygen -w internal "$@"
      '')
    ];

    home.sessionVariables = {
      SSH_SK_PROVIDER = "/usr/lib/ssh-keychain.dylib";
    };

    programs.zsh.initContent = ''
      SSH_ASKPASS_REQUIRE=force SSH_ASKPASS=true /usr/bin/ssh-add -K >/dev/null 2>&1
    '';
  };
}
