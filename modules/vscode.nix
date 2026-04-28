{
  config,
  lib,
  pkgs,
  ...
}:

lib.mkIf pkgs.stdenv.isLinux {
  programs.vscode = {
    enable = true;
    package = pkgs.vscode;
    profiles.default = {
      extensions = [ pkgs.nix-vscode-extensions.vscode-marketplace.adamgirton.gloom ];
      userSettings = {
        "workbench.colorTheme" = "Gloom";
      };
    };
  };

  systemd.user.services.vscode-serve-web = {
    Unit = {
      Description = "Visual Studio Code serve-web";
      After = [ "default.target" ];
    };

    Service = {
      ExecStart = "${lib.getExe pkgs.vscode} serve-web --host 127.0.0.1 --port 8000 --without-connection-token --accept-server-license-terms --disable-telemetry";
      Restart = "on-failure";
      RestartSec = 5;
    };

    Install = {
      WantedBy = [ "default.target" ];
    };
  };
}
