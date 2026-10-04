{
  config,
  lib,
  pkgs,
  unstablePkgs,
  ...
}:

let
  user = {
    name = "Emma Hamilton";
    email = "git@emmas.town";
  };
in
{
  programs.git = {
    enable = true;
    ignores = [
      ".pnpm"
      ".pnpm-store"
    ];
    settings = {
      user.name = user.name;
      user.email = user.email;
      url."git@github.com:".pushInsteadOf = "https://github.com/";
    };
  };

  home.packages = lib.optionals pkgs.stdenv.isDarwin [ pkgs.watchman ];

  programs.jujutsu = {
    enable = true;
    package = unstablePkgs.jujutsu;
    settings =
      lib.recursiveUpdate
        {
          user.name = user.name;
          user.email = user.email;
          ui."default-command" = "log";
          ui.pager = "${lib.getExe pkgs.less} -FR";
        }
        (
          lib.optionalAttrs pkgs.stdenv.isDarwin {
            fsmonitor.backend = "watchman";
            fsmonitor.watchman."register-snapshot-trigger" = true;
          }
          // lib.optionalAttrs (config.home.sessionVariables ? EDITOR) {
            ui.editor = config.home.sessionVariables.EDITOR;
          }
        );
  };
}
