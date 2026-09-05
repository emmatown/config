{
  lib,
  pkgs,
  modulesPath,
  ...
}:
{
  imports = [ (modulesPath + "/profiles/qemu-guest.nix") ];
  networking.hostName = lib.mkDefault "minivm-dev";
  networking.useDHCP = false;
  networking.firewall.enable = true;
  boot.loader.systemd-boot.enable = true;
  boot.loader.efi.canTouchEfiVariables = false;
  boot.kernelParams = [ "console=ttyS0,115200" ];
  boot.initrd.availableKernelModules = [
    "virtio_pci"
    "virtio_blk"
    "virtio_net"
  ];
  boot.kernelModules = [ "vmw_vsock_virtio_transport" ];
  fileSystems."/" = {
    device = "/dev/disk/by-label/nixos";
    fsType = "ext4";
  };
  fileSystems."/boot" = {
    device = "/dev/disk/by-label/ESP";
    fsType = "vfat";
  };
  services.openssh = {
    enable = true;
    openFirewall = false;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
      PermitRootLogin = "prohibit-password";
    };
  };
  # Host-only administrative transport; no IP interface is needed for the boot test.
  # systemd-ssh-generator also discovers vsock. Mask its socket so our
  # explicitly configured SSH transport remains the sole owner of port 22.
  systemd.sockets.sshd-vsock.enable = false;
  systemd.sockets.minivm-vsock-ssh = {
    wantedBy = [ "sockets.target" ];
    socketConfig = {
      ListenStream = "vsock::22";
      Accept = true;
    };
  };
  systemd.services."minivm-vsock-ssh@" = {
    requires = [ "sshd.service" ];
    after = [ "sshd.service" ];
    serviceConfig = {
      ExecStart = "${pkgs.openssh}/bin/sshd -i -f /etc/ssh/sshd_config";
      StandardInput = "socket";
      StandardError = "journal";
    };
  };
  users.users.root.openssh.authorizedKeys.keys = import ../../authorized-keys.nix;
  nix.settings = {
    experimental-features = [
      "nix-command"
      "flakes"
    ];
    extra-substituters = [ "https://cache.numtide.com" ];
    extra-trusted-public-keys = [
      "niks3.numtide.com-1:DTx8wZduET09hRmMtKdQDxNNthLQETkc/yaX7M4qK0g="
    ];
  };
  environment.systemPackages = [
    pkgs.jujutsu
    pkgs.curl
  ];
  # No auto-login, static passwords, or network interfaces in the boot prototype.
  # machine-id and SSH host keys are generated in each instance's writable root.
  system.stateVersion = "26.05";
}
