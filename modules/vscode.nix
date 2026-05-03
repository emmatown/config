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

    Service =
      let
        vscodeDataDir = "${config.home.homeDirectory}/.vscode";
        vscodeExtensionsDir = "${vscodeDataDir}/extensions";
        vscodeUserDataDir = "${config.xdg.configHome}/${config.programs.vscode.nameShort}";
        vscodeCliDataDir = "${vscodeDataDir}/cli";
        vscodeServeWebDataDir = "${vscodeDataDir}/serve-web";
      in
      {
        ExecStart = "${lib.getExe pkgs.vscode} --user-data-dir ${vscodeUserDataDir} --extensions-dir ${vscodeExtensionsDir} serve-web --cli-data-dir ${vscodeCliDataDir} --server-data-dir ${vscodeServeWebDataDir} --host 127.0.0.1 --port 8000 --without-connection-token --accept-server-license-terms --disable-telemetry";
        Restart = "on-failure";
        RestartSec = 5;
      };

    Install = {
      WantedBy = [ "default.target" ];
    };
  };
}
