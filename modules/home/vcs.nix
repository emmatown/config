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
    ignores = [ ".pnpm" ];
    settings = {
      user.name = user.name;
      user.email = user.email;
      url."git@github.com:".pushInsteadOf = "https://github.com/";
    };
  };

  home.packages = with pkgs; [ watchman ];

  programs.jujutsu = {
    enable = true;
    package = unstablePkgs.jujutsu;
    settings =
      lib.recursiveUpdate
        {
          user.name = user.name;
          user.email = user.email;
          ui."default-command" = "log";
          ui.pager = "less -FR";
          fsmonitor.backend = "watchman";
          fsmonitor.watchman."register-snapshot-trigger" = true;
        }
        (
          lib.optionalAttrs (config.home.sessionVariables ? EDITOR) {
            ui.editor = config.home.sessionVariables.EDITOR;
          }
        );
  };

  programs.starship = lib.mkIf config.programs.starship.enable {
    extraPackages = [ pkgs.jj-starship ];
    settings = {
      custom.jj = {
        when = "jj-starship detect";
        shell = [
          "jj-starship"
          "--no-symbol"
        ];
        format = "$output ";
      };
      git_branch.disabled = true;
      git_status.disabled = true;
    };
  };

}
