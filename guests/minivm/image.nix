{
  pkgs,
  config,
  lib,
  name ? "minivm-development-image",
  diskSize ? 16384,
}:
let
  lklMemoryOverlay = _final: prev: {
    lkl = prev.lkl.overrideAttrs (old: {
      postPatch = (old.postPatch or "") + ''
        # Increase LKL kernel memory for large disk image builds.
        substituteInPlace tools/lkl/cptofs.c \
          --replace-fail 'lkl_start_kernel("mem=100M")' 'lkl_start_kernel("mem=1024M")'
      '';
    });
  };

  imagePkgs = pkgs.extend lklMemoryOverlay;
in
import (pkgs.path + "/nixos/lib/make-disk-image.nix") {
  pkgs = imagePkgs;
  inherit config lib;
  inherit name diskSize;
  memSize = 2048;
  format = "raw";
  partitionTableType = "efi";
  bootSize = "1G";
  copyChannel = false;
  # Do not embed a fake configuration.nix that would misrepresent template source.
  configFile = null;
}
