# Five Linux VMs exercise the actual gateway and host IP rules without internet.
{
  pkgs,
  lib,
  gatewayConfig,
  hostConfig,
  inferenceConfig,
}:

let
  renameInterfaces =
    replacements: tables:
    lib.mapAttrs (_: table: {
      inherit (table) family;
      content =
        lib.replaceStrings (builtins.attrNames replacements) (builtins.attrValues replacements)
          table.content;
    }) tables;
  tools = { pkgs, ... }: {
    environment.systemPackages = [
      pkgs.curl
      pkgs.iproute2
      pkgs.dnsutils
    ];
    networking.firewall.enable = false;
    networking.useDHCP = false;
  };
in
pkgs.testers.runNixOSTest {
  name = "vm-workspace-network-policy";
  nodes = {
    dev = { ... }: {
      imports = [ tools ];
      virtualisation.vlans = [
        1
        4
      ];
      networking.interfaces.eth2.ipv4.addresses = [
        {
          address = "192.168.126.2";
          prefixLength = 30;
        }
      ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "192.168.127.2";
          prefixLength = 30;
        }
      ];
      networking.defaultGateway = "192.168.127.1";
      networking.nameservers = [ "192.168.127.1" ];
    };
    gateway = { ... }: {
      imports = [ tools ];
      virtualisation.vlans = [
        1
        2
      ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "192.168.127.1";
          prefixLength = 30;
        }
      ];
      networking.interfaces.eth2.ipv4.addresses = [
        {
          address = "172.31.255.2";
          prefixLength = 30;
        }
      ];
      networking.defaultGateway = "172.31.255.1";
      boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
      networking.nftables.enable = true;
      networking.nftables.tables = renameInterfaces {
        "\"lan\"" = "\"eth1\"";
        "\"wan\"" = "\"eth2\"";
      } gatewayConfig.networking.nftables.tables;
      services.unbound = {
        enable = true;
        settings = gatewayConfig.services.unbound.settings // {
          # The synthetic DNS server has no DNSSEC chain for this test zone.
          server = gatewayConfig.services.unbound.settings.server // {
            domain-insecure = [ "public.test" ];
            # Unbound otherwise answers NXDOMAIN for its built-in .test zone
            # before consulting any forwarder. Keep this override test-only.
            local-zone = [ ''"test." nodefault'' ];
          };
          # There is no real internet in this test. Send the synthetic zone
          # only to the fake resolver, not the production fallback resolver.
          forward-zone = gatewayConfig.services.unbound.settings.forward-zone ++ [
            {
              name = "public.test";
              forward-addr = [ "1.1.1.1" ];
            }
          ];
        };
      };
    };
    mini = { ... }: {
      imports = [ tools ];
      virtualisation.vlans = [
        2
        3
      ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "172.31.255.1";
          prefixLength = 29;
        }
      ];
      networking.interfaces.eth2.ipv4.addresses = [
        {
          address = "11.0.0.2";
          prefixLength = 24;
        }
      ];
      networking.defaultGateway = "11.0.0.1";
      boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
      networking.nftables.enable = true;
      networking.nftables.tables =
        renameInterfaces
          {
            "\"vm-wan\"" = "\"eth1\"";
            "\"eno1\"" = "\"eth2\"";
          }
          (
            lib.filterAttrs (
              name: _:
              builtins.elem name [
                "vm-boundary"
                "vm-uplink-nat"
              ]
            ) hostConfig.networking.nftables.tables
          );
      services.nginx.enable = true;
      services.nginx.virtualHosts.default.locations."/".return = "200 'host service'";
    };
    outside = { ... }: {
      imports = [ tools ];
      virtualisation.vlans = [ 3 ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "11.0.0.1";
          prefixLength = 24;
        }
        {
          address = "1.1.1.1";
          prefixLength = 32;
        }
        {
          address = "192.168.0.254";
          prefixLength = 32;
        }
        {
          address = "169.254.169.254";
          prefixLength = 32;
        }
      ];
      services.nginx.enable = true;
      services.nginx.virtualHosts.default.listen = [
        {
          addr = "0.0.0.0";
          port = 80;
        }
        {
          addr = "0.0.0.0";
          port = 443;
        }
      ];
      services.nginx.virtualHosts.default.locations."/".return = "200 'public service'";
      services.dnsmasq = {
        enable = true;
        settings = {
          no-resolv = true;
          address = [ "/public.test/11.0.0.1" ];
          local = [ "/public.test/" ];
        };
      };
    };
    inference = { ... }: {
      imports = [ tools ];
      virtualisation.vlans = [
        4
        2
      ];
      networking.interfaces.eth1.ipv4.addresses = [
        {
          address = "192.168.126.1";
          prefixLength = 30;
        }
      ];
      networking.interfaces.eth2.ipv4.addresses = [
        {
          address = "172.31.255.6";
          prefixLength = 29;
        }
      ];
      networking.defaultGateway = "172.31.255.1";
      networking.nftables.enable = true;
      networking.nftables.tables = renameInterfaces {
        "\"client\"" = "\"eth1\"";
        "\"wan\"" = "\"eth2\"";
      } inferenceConfig.networking.nftables.tables;
      # Fake inference endpoint: Go tests separately exercise real broker logic.
      services.nginx.enable = true;
      services.nginx.virtualHosts.default.listen = [
        {
          addr = "0.0.0.0";
          port = 8080;
        }
      ];
      services.nginx.virtualHosts.default.locations."/".return = "200 'inference endpoint'";
    };
  };

  testScript = ''
    start_all()
    gateway.wait_for_unit("unbound.service")
    gateway.wait_for_unit("nftables.service")
    mini.wait_for_unit("nftables.service")
    mini.wait_for_unit("nginx.service")
    outside.wait_for_unit("nginx.service")
    outside.wait_for_unit("dnsmasq.service")
    inference.wait_for_unit("nftables.service")
    inference.wait_for_unit("nginx.service")
    dev.succeed("ip route get 11.0.0.1 | grep 'via 192.168.127.1'")
    dev.wait_until_succeeds("curl --noproxy '*' -fsS --max-time 3 http://11.0.0.1")
    gateway.wait_until_succeeds("dig @1.1.1.1 public.test A +short +time=2 +tries=1 | grep -Fx 11.0.0.1")
    dev.wait_until_succeeds("dig @192.168.127.1 public.test A +short +time=2 +tries=1 | grep -Fx 11.0.0.1")
    dev.succeed("curl --noproxy '*' -fsS --max-time 5 http://public.test")
    dev.succeed("curl --noproxy '*' -fsS --max-time 3 http://192.168.126.1:8080")
    dev.fail("curl --noproxy '*' -fsS --max-time 2 http://192.168.126.1:22")
    inference.succeed("curl --noproxy '*' -fsS --max-time 3 http://11.0.0.1:443")
    inference.fail("curl --noproxy '*' -fsS --max-time 2 http://172.31.255.1")
    mini.fail("curl --noproxy '*' -fsS --max-time 2 http://172.31.255.6:8080")

    for address in ["172.31.255.1", "192.168.0.254", "169.254.169.254"]:
        dev.fail(f"curl --noproxy '*' -fsS --max-time 2 http://{address}")

    # Spoofing an uplink address on the development segment must not bypass policy.
    dev.succeed("ip address add 172.31.255.2/32 dev eth1")
    dev.fail("curl --noproxy '*' --interface 172.31.255.2 -fsS --max-time 2 http://11.0.0.1")
    dev.succeed("ip address del 172.31.255.2/32 dev eth1")

    # Simulate a compromised gateway: remove its output restrictions. Host
    # enforcement still blocks private destinations and the host's own service.
    gateway.succeed("nft flush chain inet vm-policy output")
    gateway.succeed("nft add rule inet vm-policy output accept")
    gateway.succeed("curl --noproxy '*' -fsS --max-time 3 http://11.0.0.1")
    gateway.fail("curl --noproxy '*' -fsS --max-time 2 http://192.168.126.1:8080")
    for address in ["172.31.255.1", "192.168.0.254", "169.254.169.254"]:
        gateway.fail(f"curl --noproxy '*' -fsS --max-time 2 http://{address}")

    gateway.shutdown()
    dev.fail("curl --noproxy '*' -fsS --max-time 2 http://11.0.0.1")
    dev.succeed("curl --noproxy '*' -fsS --max-time 3 http://192.168.126.1:8080")
  '';
}
