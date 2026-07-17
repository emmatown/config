{ modulesPath, ... }:

let
  authorizedKeys = import ../authorized-keys.nix;
in

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
