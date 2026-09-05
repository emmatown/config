{ lib, ... }:

let
  blocked = lib.concatStringsSep ", " (import ./public-egress.nix);
in
{
  imports = [ ./development.nix ];

  networking.hostName = lib.mkForce "vm-gateway";
  networking.enableIPv6 = false;
  networking.useNetworkd = true;
  # The rules below own all IP input, output, and forwarding in this guest.
  networking.firewall.enable = lib.mkForce false;
  networking.nftables.enable = true;
  boot.kernel.sysctl."net.ipv4.ip_forward" = 1;

  systemd.network.links = {
    "10-lan" = {
      matchConfig.MACAddress = "02:00:00:00:01:01";
      linkConfig.Name = "lan";
    };
    "10-wan" = {
      matchConfig.MACAddress = "02:00:00:00:02:02";
      linkConfig.Name = "wan";
    };
  };
  systemd.network.networks = {
    "20-lan" = {
      matchConfig.Name = "lan";
      address = [ "192.168.127.1/30" ];
      networkConfig.LinkLocalAddressing = "no";
    };
    "20-wan" = {
      matchConfig.Name = "wan";
      address = [ "172.31.255.2/30" ];
      routes = [ { Gateway = "172.31.255.1"; } ];
      networkConfig.LinkLocalAddressing = "no";
    };
  };

  services.unbound = {
    enable = true;
    settings = {
      server = {
        interface = [
          "127.0.0.1"
          "192.168.127.1"
        ];
        access-control = [
          "127.0.0.0/8 allow"
          "192.168.127.2/32 allow"
        ];
        do-ip6 = false;
        ip-freebind = true;
        private-address = import ./public-egress.nix;
      };
      forward-zone = [
        {
          name = ".";
          forward-addr = [
            "1.1.1.1"
            "9.9.9.9"
          ];
        }
      ];
    };
  };

  networking.nftables.tables = {
    vm-policy = {
      family = "inet";
      content = ''
        set blocked { type ipv4_addr; flags interval; elements = { ${blocked} }; }
        chain input {
          type filter hook input priority filter; policy drop;
          iifname "lo" accept
          meta nfproto ipv6 drop
          ct state invalid drop
          iifname "wan" ct state established,related accept
          iifname "lan" ip saddr 192.168.127.2 udp dport 53 accept
          iifname "lan" ip saddr 192.168.127.2 tcp dport 53 accept
        }
        chain forward {
          type filter hook forward priority filter; policy drop;
          meta nfproto ipv6 drop
          ct state invalid drop
          iifname "lan" ip saddr != 192.168.127.2 drop
          iifname "lan" ip daddr @blocked counter drop
          iifname "lan" oifname "wan" accept
          iifname "wan" oifname "lan" ct state established,related accept
        }
        chain output {
          type filter hook output priority filter; policy drop;
          oifname "lo" accept
          meta nfproto ipv6 drop
          oifname "lan" ip daddr 192.168.127.2 ct state established,related accept
          oifname "wan" ip daddr @blocked drop
          oifname "wan" ip daddr { 1.1.1.1, 9.9.9.9 } udp dport 53 accept
          oifname "wan" ip daddr { 1.1.1.1, 9.9.9.9 } tcp dport 53 accept
          oifname "wan" udp dport 123 accept
        }
      '';
    };
    vm-nat = {
      family = "ip";
      content = ''
        chain postrouting {
          type nat hook postrouting priority srcnat; policy accept;
          oifname "wan" ip saddr 192.168.127.2 masquerade
        }
      '';
    };
  };
}
