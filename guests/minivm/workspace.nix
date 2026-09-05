{
  codexPackage,
  lib,
  pkgs,
  ...
}:

let
  policy = import ./inference-policy.nix;
  codexConfig = pkgs.writeText "workspace-codex-config" ''
    model = "${policy.model}"
    model_provider = "vm-broker"
    approval_policy = "never"
    sandbox_mode = "danger-full-access"

    [model_providers.vm-broker]
    name = "Isolated inference broker"
    base_url = "http://192.168.126.1:8080/v1"
    wire_api = "responses"
    requires_openai_auth = false
    supports_websockets = false
  '';
in
{
  imports = [ ./development.nix ];

  networking.hostName = lib.mkForce "vm-dev";
  networking.enableIPv6 = false;
  networking.useNetworkd = true;
  networking.nameservers = [ "192.168.127.1" ];
  systemd.network.networks."20-development" = {
    matchConfig.MACAddress = "02:00:00:00:01:02";
    address = [ "192.168.127.2/30" ];
    routes = [ { Gateway = "192.168.127.1"; } ];
    networkConfig.LinkLocalAddressing = "no";
    linkConfig.RequiredForOnline = "routable";
  };
  systemd.network.networks."20-inference" = {
    matchConfig.MACAddress = "02:00:00:00:03:02";
    address = [ "192.168.126.2/30" ];
    networkConfig.LinkLocalAddressing = "no";
    linkConfig.RequiredForOnline = "no";
  };
  systemd.tmpfiles.rules = [
    "d /root/.codex 0700 root root -"
    "C /root/.codex/config.toml 0600 root root - ${codexConfig}"
  ];
  environment.etc."codex/broker-config.toml".source = codexConfig;
  environment.etc."jj/config.toml".text = ''
    [user]
    name = "Emma Hamilton"
    email = "git@emmas.town"
  '';

  boot.kernelModules = [
    "kvm-intel"
    "btrfs"
  ];
  fileSystems."/workspace" = {
    device = "/dev/disk/by-label/vm-workspace";
    fsType = "btrfs";
    options = [
      "compress=zstd"
      "noatime"
    ];
  };

  # These tools are available before the first network connection or nix develop.
  environment.systemPackages = [
    codexPackage
    pkgs.go
    pkgs.gcc
    pkgs.sqlite
    pkgs.pkg-config
    pkgs.git
    pkgs.ripgrep
    pkgs.jq
    pkgs.vim
    pkgs.btrfs-progs
    pkgs.cloud-hypervisor
    pkgs.socat
    pkgs.python3
  ];
  environment.variables = {
    GOPROXY = "off";
    GOTOOLCHAIN = "local";
  };
}
