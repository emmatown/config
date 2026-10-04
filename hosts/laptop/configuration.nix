{ pkgs, ... }:

{
  imports = [
    ./disk-config.nix
    ./hardware-configuration.nix
    ./framework.nix
  ];

  networking = {
    hostName = "emma-laptop";
    networkmanager.enable = true;
  };
  time.timeZone = "Australia/Brisbane";

  nix.settings.experimental-features = [
    "nix-command"
    "flakes"
  ];

  boot = {
    kernelPackages = pkgs.linuxPackages_latest;
    initrd.systemd.enable = true;
    loader = {
      systemd-boot.enable = true;
      efi.canTouchEfiVariables = true;
    };
  };

  services = {
    desktopManager.cosmic.enable = true;
    displayManager.cosmic-greeter.enable = true;
    flatpak.enable = true;
    # Allow the logged-in user to access FIDO security keys for OpenSSH.
    udev.packages = [ pkgs.libfido2 ];
    power-profiles-daemon.enable = true;
    fstrim.enable = true;
    btrfs.autoScrub.enable = true;
    pipewire = {
      enable = true;
      alsa.enable = true;
      alsa.support32Bit = true;
      pulse.enable = true;
    };
  };
  security.rtkit.enable = true;

  environment.systemPackages = [
    pkgs.bazaar
    pkgs.yubikey-manager
  ];

  # Register Flathub for Bazaar and other Flatpak clients on first boot.
  systemd.services.flatpak-flathub = {
    description = "Add the Flathub Flatpak remote";
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" ];
    after = [ "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      RemainAfterExit = true;
      ExecStart = "${pkgs.flatpak}/bin/flatpak remote-add --system --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo";
      Restart = "on-failure";
      RestartSec = "30s";
    };
  };

  hardware = {
    enableRedistributableFirmware = true;
    bluetooth.enable = true;
  };
  zramSwap.enable = true;

  # nixos-enter --root /mnt -c 'passwd emma'
  users.users.emma = {
    isNormalUser = true;
    extraGroups = [
      "wheel"
      "networkmanager"
    ];
  };

  system.stateVersion = "26.05";
}
