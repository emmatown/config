{ inputs, pkgs, ... }:

{
  programs.zsh.enable = true;
  users.users.emma.shell = pkgs.zsh;

  home-manager = {
    useGlobalPkgs = true;
    useUserPackages = true;
    extraSpecialArgs = {
      inherit inputs;
      unstablePkgs = inputs.nixpkgs-unstable.legacyPackages.${pkgs.stdenv.hostPlatform.system};
    };
    users.emma =
      { config, ... }:
      let
        # The live checkout keeps COSMIC settings writable from its UI.
        cosmicConfig = "${config.home.homeDirectory}/Projects/config/hosts/laptop";
      in
      {
        imports = [ ../../home.nix ];
        home.username = "emma";
        home.packages = [ pkgs.nano ];
        home.sessionVariables.EDITOR = "nano";
        profiles.javascript.enable = false;

        # Link the directory because COSMIC atomically replaces settings files.
        xdg.configFile."cosmic/com.system76.CosmicComp/v1".source =
          config.lib.file.mkOutOfStoreSymlink "${cosmicConfig}/cosmic/com.system76.CosmicComp/v1";
        home.file.".config/cosmic-initial-setup-done".source =
          config.lib.file.mkOutOfStoreSymlink "${cosmicConfig}/cosmic-initial-setup-done";
      };
  };
}
