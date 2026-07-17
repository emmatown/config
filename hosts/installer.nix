{ modulesPath, ... }:

let
  authorizedKeys = import ../authorized-keys.nix;
in
# nix build .#nixosConfigurations.installer.config.system.build.isoImage

# mac
# diskutil list
# diskutil unmountDisk /dev/diskN
# sudo dd if=result/iso/blah.iso of=/dev/rdiskN bs=4m status=progress
{
  imports = [
    "${modulesPath}/installer/cd-dvd/installation-cd-minimal.nix"
  ];

  boot.zfs.forceImportRoot = false;

  networking.hostName = "nixos-installer";

  services.avahi = {
    enable = true;
    nssmdns4 = true;
    openFirewall = true;
    publish = {
      enable = true;
      addresses = true;
      workstation = true;
    };
  };

  services.openssh = {
    enable = true;
    openFirewall = true;
    settings = {
      KbdInteractiveAuthentication = false;
      PasswordAuthentication = false;
      PermitRootLogin = "prohibit-password";
    };
  };

  users.users.root.openssh.authorizedKeys.keys = authorizedKeys;
}
