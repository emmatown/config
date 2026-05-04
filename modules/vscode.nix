{
  config,
  lib,
  pkgs,
  ...
}:

lib.mkIf pkgs.stdenv.isLinux {
  programs.vscode = {
    enable = true;
    package = pkgs.openvscode-server;
    profiles.default = {
      extensions = [ pkgs.nix-vscode-extensions.vscode-marketplace.adamgirton.gloom ];
      userSettings = {
        "workbench.colorTheme" = "Gloom";
      };
    };
  };

  systemd.user.services.vscode-serve-web = {
    Unit = {
      Description = "OpenVSCode Server";
      After = [ "default.target" ];
    };

    Service =
      let
        userDataDir = "${config.xdg.configHome}/${config.programs.vscode.nameShort}";
      in
      {
        ExecStart = "${lib.getExe config.programs.vscode.package} --host 127.0.0.1 --port 8000 --without-connection-token --accept-server-license-terms --telemetry-level off --user-data-dir ${userDataDir}";
        Restart = "on-failure";
        RestartSec = 5;
      };

    Install = {
      WantedBy = [ "default.target" ];
    };
  };
}
