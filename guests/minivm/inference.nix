{
  codexPackage,
  lib,
  pkgs,
  ...
}:

let
  broker = pkgs.callPackage ../../modules/minivm/broker-package.nix { };
  policy = import ./inference-policy.nix;
  blocked = lib.concatStringsSep ", " (import ./public-egress.nix);
in
{
  imports = [ ./development.nix ];
  networking.hostName = lib.mkForce "vm-inference";
  networking.enableIPv6 = false;
  networking.useNetworkd = true;
  networking.nameservers = [
    "1.1.1.1"
    "9.9.9.9"
  ];
  networking.firewall.enable = lib.mkForce false;
  networking.nftables.enable = true;
  boot.kernel.sysctl."net.ipv4.ip_forward" = 0;

  systemd.network.links = {
    "10-client" = {
      matchConfig.MACAddress = "02:00:00:00:03:01";
      linkConfig.Name = "client";
    };
    "10-wan" = {
      matchConfig.MACAddress = "02:00:00:00:02:06";
      linkConfig.Name = "wan";
    };
  };
  systemd.network.networks = {
    "20-client" = {
      matchConfig.Name = "client";
      address = [ "192.168.126.1/30" ];
      networkConfig.LinkLocalAddressing = "no";
    };
    "20-wan" = {
      matchConfig.Name = "wan";
      address = [ "172.31.255.6/29" ];
      routes = [ { Gateway = "172.31.255.1"; } ];
      networkConfig.LinkLocalAddressing = "no";
    };
  };

  networking.nftables.tables.inference-policy = {
    family = "inet";
    content = ''
      set blocked { type ipv4_addr; flags interval; elements = { ${blocked} }; }
      chain input {
        type filter hook input priority filter; policy drop;
        iifname "lo" accept
        meta nfproto ipv6 drop
        ct state invalid drop
        iifname "client" ip saddr 192.168.126.2 tcp dport 8080 accept
        iifname "wan" ct state established,related accept
      }
      chain forward {
        type filter hook forward priority filter; policy drop;
      }
      chain output {
        type filter hook output priority filter; policy drop;
        oifname "lo" accept
        meta nfproto ipv6 drop
        oifname "client" ip daddr 192.168.126.2 ct state established,related accept
        oifname "wan" ip daddr @blocked drop
        oifname "wan" tcp dport 443 accept
        oifname "wan" ip daddr { 1.1.1.1, 9.9.9.9 } udp dport 53 accept
        oifname "wan" ip daddr { 1.1.1.1, 9.9.9.9 } tcp dport 53 accept
        oifname "wan" udp dport 123 accept
      }
    '';
  };

  users.groups.codex-broker = { };
  users.users.codex-broker = {
    isSystemUser = true;
    group = "codex-broker";
    home = "/var/lib/codex-broker";
  };
  environment.systemPackages = [
    codexPackage
    broker
  ];
  systemd.tmpfiles.rules = [
    "d /var/lib/codex-broker 0700 codex-broker codex-broker -"
    "C /var/lib/codex-broker/config.toml 0600 codex-broker codex-broker - ${pkgs.writeText "broker-codex-config" ''
      cli_auth_credentials_store = "file"
    ''}"
  ];
  systemd.services.vm-inference-broker = {
    description = "Codex inference broker with VM-local ChatGPT credentials";
    wantedBy = [ "multi-user.target" ];
    wants = [ "network-online.target" ];
    after = [
      "network-online.target"
      "nftables.service"
    ];
    requires = [ "nftables.service" ];
    serviceConfig = {
      User = "codex-broker";
      Group = "codex-broker";
      StateDirectory = "codex-broker";
      StateDirectoryMode = "0700";
      UMask = "0077";
      ExecStart = "${broker}/bin/vm-inference-broker --concurrency ${toString policy.concurrency}";
      Restart = "on-failure";
      RestartSec = 3;
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectHome = true;
      PrivateTmp = true;
      PrivateDevices = true;
      ProtectKernelTunables = true;
      ProtectKernelModules = true;
      ProtectControlGroups = true;
      CapabilityBoundingSet = "";
      RestrictAddressFamilies = [
        "AF_INET"
        "AF_UNIX"
      ];
      LimitCORE = 0;
      MemoryMax = "512M";
      TasksMax = 128;
    };
  };
}
