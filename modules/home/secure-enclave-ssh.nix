{ lib, pkgs, ... }:

{
  config = lib.mkIf pkgs.stdenv.isDarwin {
    home.sessionVariables = {
      SSH_SK_PROVIDER = "/usr/lib/ssh-keychain.dylib";
    };

    programs.zsh.initContent = ''
      SSH_ASKPASS_REQUIRE=force SSH_ASKPASS=true /usr/bin/ssh-add -K >/dev/null 2>&1
    '';
  };
}
