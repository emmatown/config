{ lib, pkgs, ... }:

let
  authorizedKeys = import ../../authorized-keys.nix;
in

{
  imports = [
    ./disk-config.nix
    ./hardware-configuration.nix
    ./ssh-tpm-host-keys.nix
  ];

  time.timeZone = "Australia/Brisbane";

  nix.settings.experimental-features = [
    "nix-command"
    "flakes"
  ];

  boot = {
    initrd = {
      # sudo systemd-cryptenroll --wipe-slot=tpm2 --tpm2-device=auto --tpm2-pcrs='7:sha256+15:sha256=0000000000000000000000000000000000000000000000000000000000000000' /dev/nvme0n1p
      luks.devices.crypted.crypttabExtraOpts = [
        "tpm2-device=auto"
        "tpm2-measure-pcr=yes"
        "tpm2-measure-bank=sha256"
      ];
    };
    loader = {
      # Lanzaboote replaces the regular systemd-boot installer and signs each
      # boot generation with the keys stored outside the Nix store.
      systemd-boot.enable = lib.mkForce false;
      efi.canTouchEfiVariables = true;
    };
    lanzaboote = {
      enable = true;
      pkiBundle = "/var/lib/sbctl";
    };
    zfs.forceImportRoot = false;
  };

  environment.systemPackages = [ pkgs.sbctl ];

  networking = {
    hostName = "emma-mini";
    useNetworkd = true;
    useDHCP = true;
    interfaces.eno1.wakeOnLan.enable = true;
  };

  services.avahi = {
    enable = true;
    openFirewall = true;
    publish = {
      enable = true;
      addresses = true;
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

  services.fstrim.enable = true;

  users = {
    users = {
      emma = {
        isNormalUser = true;
        extraGroups = [
          "wheel"
        ];
        openssh.authorizedKeys.keys = authorizedKeys;
      };

      root.openssh.authorizedKeys.keys = authorizedKeys;
    };
  };

  security.sudo.wheelNeedsPassword = false;

  system.stateVersion = "26.05";
}
