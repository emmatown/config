{
  pkgs,
  lib,
  config,
  ...
}:
{
  home.packages = with pkgs; [
    (rustPlatform.buildRustPackage rec {
      pname = "pyn";
      version = "0.6.1";

      src = fetchCrate {
        inherit pname version;
        sha256 = "sha256-6Y3YLj/I26R92ZFH7punC4YHbPyDd0l8UdJ4nj+JyjM=";
      };
      doCheck = false;
      cargoHash = "sha256-KwCn2oK96sMdTFB8wq+lUlWF/uQ1wgqbWWHHSfdBikI=";
    })
    proto
  ];
  programs.zsh = {
    initContent = ''
      eval "$(proto activate zsh)"
    '';
    shellAliases = {
      r = "pyn";
    };
  };
}
