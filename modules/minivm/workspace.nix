# Bootstrap infrastructure independent of the controller being developed.
{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.vmWorkspace;
  state = "/var/lib/vm-state/bootstrap";
  blocked = lib.concatStringsSep ", " (import ../../guests/minivm/public-egress.nix);
  firmware = pkgs.OVMF-cloud-hypervisor.firmware;

  machines = {
    dev = {
      image = cfg.image;
      memory = 8192;
      cpus = 4;
      nested = "on";
      nets = [
        "tap=vm-d-tap,mac=02:00:00:00:01:02,num_queues=2"
        "tap=vm-d-inf,mac=02:00:00:00:03:02,num_queues=2"
      ];
    };
    inference = {
      image = cfg.inferenceImage;
      memory = 1024;
      cpus = 1;
      nested = "off";
      nets = [
        "tap=vm-i-client,mac=02:00:00:00:03:01,num_queues=2"
        "tap=vm-i-wan,mac=02:00:00:00:02:06,num_queues=2"
      ];
    };
    gateway = {
      image = cfg.gatewayImage;
      memory = 1024;
      cpus = 1;
      nested = "off";
      nets = [
        "tap=vm-g-lan,mac=02:00:00:00:01:01,num_queues=2"
        "tap=vm-g-wan,mac=02:00:00:00:02:02,num_queues=2"
      ];
    };
  };

  prepare =
    name: machine:
    pkgs.writeShellApplication {
      name = "vm-${name}-prepare";
      runtimeInputs = with pkgs; [
        coreutils
        btrfs-progs
        util-linux
        gnugrep
      ];
      text = ''
        ${lib.optionalString (name == "dev") ''
          if ! grep -Eq '^(Y|1)$' /sys/module/kvm_intel/parameters/nested; then
            echo "Intel nested KVM must be enabled before starting the workspace" >&2
            exit 1
          fi
        ''}
        base=${state}/${name}
        install -d -m 0711 ${state}
        if [ ! -d "$base" ]; then
          btrfs subvolume create "$base"
        fi
        if [ ! -e "$base/root.raw" ]; then
          cp --reflink=always --sparse=auto ${machine.image}/nixos.img "$base/root.partial"
          chmod 0600 "$base/root.partial"
          sync "$base/root.partial"
          mv "$base/root.partial" "$base/root.raw"
        fi
        ${lib.optionalString (name == "dev") ''
          if [ ! -e "$base/workspace.raw" ]; then
            truncate -s 64G "$base/workspace.partial"
            mkfs.btrfs -f -L vm-workspace "$base/workspace.partial"
            chmod 0600 "$base/workspace.partial"
            sync "$base/workspace.partial"
            mv "$base/workspace.partial" "$base/workspace.raw"
          fi
        ''}
        chown root:vm-${name} "$base"
        chmod 0750 "$base"
        chown vm-${name}:vm-${name} "$base/"*.raw
        # The VM may write its disks, but cannot replace files in the parent.
        chmod 0711 ${state}
        sync "$base"
      '';
    };

  stop = pkgs.writeShellApplication {
    name = "vm-graceful-stop";
    runtimeInputs = [
      pkgs.cloud-hypervisor
      pkgs.coreutils
    ];
    text = ''
      ch-remote --api-socket "$1" power-button || true
      for _ in $(seq 1 90); do
        if ! kill -0 "$2" 2>/dev/null; then
          exit 0
        fi
        sleep 1
      done
      echo "Guest did not shut down within 90 seconds" >&2
      exit 1
    '';
  };

  vmService = name: machine: {
    description = "Isolated ${name} VM for platform development";
    wantedBy = [ "multi-user.target" ];
    requires = [
      "vm-${name}-prepare.service"
      "systemd-networkd.service"
      "nftables.service"
    ];
    after = [
      "vm-${name}-prepare.service"
      "systemd-networkd.service"
      "nftables.service"
    ];
    bindsTo = [ "nftables.service" ];
    unitConfig.RequiresMountsFor = [ state ];
    serviceConfig = {
      User = "vm-${name}";
      Group = "vm-${name}";
      SupplementaryGroups = [ "kvm" ];
      RuntimeDirectory = "vm-${name}";
      RuntimeDirectoryMode = "0700";
      ExecStart = lib.escapeShellArgs (
        [
          "${pkgs.cloud-hypervisor}/bin/cloud-hypervisor"
          "--firmware"
          "${firmware}"
          "--cpus"
          "boot=${toString machine.cpus},nested=${machine.nested}"
          "--memory"
          "size=${toString machine.memory}M"
          "--disk"
          "path=${state}/${name}/root.raw"
        ]
        ++ lib.optionals (name == "dev") [ "path=${state}/${name}/workspace.raw" ]
        ++ [
          "--net"
        ]
        ++ machine.nets
        ++ [
          "--serial"
          "tty"
          "--console"
          "off"
          "--vsock"
          "cid=3,socket=/run/vm-${name}/vsock"
          "--api-socket"
          "/run/vm-${name}/vmm.sock"
        ]
      );
      ExecStop = "${stop}/bin/vm-graceful-stop /run/vm-${name}/vmm.sock $MAINPID";
      Restart = "always";
      RestartSec = 3;
      TimeoutStopSec = 100;
      StandardInput = "null";
      StandardOutput = "journal";
      StandardError = "journal";
      CPUQuota = "${toString (machine.cpus * 100)}%";
      TasksMax = 256;
      UMask = "0077";
      NoNewPrivileges = true;
      CapabilityBoundingSet = "";
      ProtectSystem = "strict";
      ProtectHome = true;
      ProtectKernelTunables = true;
      ProtectKernelModules = true;
      ProtectControlGroups = true;
      PrivateTmp = true;
      DevicePolicy = "closed";
      DeviceAllow = [
        "/dev/kvm rw"
        "/dev/net/tun rw"
      ];
      ReadWritePaths = [ "${state}/${name}" ];
      # TAP attachment uses the host namespace, but the VMM process itself must
      # not open IP connections to host services or the LAN.
      IPAddressDeny = "any";
    }
    // lib.optionalAttrs (name != "dev") {
      MemoryMax = "${toString (machine.memory + 2048)}M";
      MemorySwapMax = 0;
    };
  };

  bridge = name: {
    netdevConfig = {
      Name = name;
      Kind = "bridge";
    };
  };
  tap = name: user: {
    netdevConfig = {
      Name = name;
      Kind = "tap";
    };
    tapConfig = {
      User = user;
      Group = user;
      PacketInfo = false;
      VNetHeader = true;
      MultiQueue = false;
    };
  };
  attach = name: parent: {
    matchConfig.Name = name;
    networkConfig = {
      Bridge = parent;
      LinkLocalAddressing = "no";
    };
    linkConfig.RequiredForOnline = "no";
  };
in
{
  options.services.vmWorkspace = {
    enable = lib.mkEnableOption "the isolated development workspace and gateway";
    image = lib.mkOption { type = lib.types.package; };
    gatewayImage = lib.mkOption { type = lib.types.package; };
    inferenceImage = lib.mkOption { type = lib.types.package; };
    uplink = lib.mkOption {
      type = lib.types.strMatching "[a-zA-Z0-9_.-]+";
      default = "eno1";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
        message = "The development workspace requires x86_64 Linux and Intel nested KVM.";
      }
    ];
    boot.kernelModules = [
      "kvm-intel"
      "tun"
      "br_netfilter"
    ];
    boot.extraModprobeConfig = "options kvm_intel nested=1";
    boot.kernel.sysctl."net.ipv4.ip_forward" = 1;
    # Only the gateway routes the development segment. Avoid sending bridged
    # guest-to-gateway packets through the host's routed-IP rules.
    boot.kernel.sysctl."net.bridge.bridge-nf-call-iptables" = 0;
    boot.kernel.sysctl."net.bridge.bridge-nf-call-ip6tables" = 0;
    networking.useNetworkd = true;
    networking.nftables.enable = true;
    users.groups = lib.mapAttrs' (name: _: lib.nameValuePair "vm-${name}" { }) machines;
    users.users = lib.mapAttrs' (
      name: _:
      lib.nameValuePair "vm-${name}" {
        isSystemUser = true;
        group = "vm-${name}";
      }
    ) machines;

    systemd.network.netdevs = {
      "30-vm-inf" = bridge "vm-inf";
      "40-vm-d-inf" = tap "vm-d-inf" "vm-dev";
      "40-vm-i-client" = tap "vm-i-client" "vm-inference";
      "40-vm-i-wan" = tap "vm-i-wan" "vm-inference";
      "30-vm-dev" = bridge "vm-dev";
      "30-vm-wan" = bridge "vm-wan";
      "40-vm-d-tap" = tap "vm-d-tap" "vm-dev";
      "40-vm-g-lan" = tap "vm-g-lan" "vm-gateway";
      "40-vm-g-wan" = tap "vm-g-wan" "vm-gateway";
    };
    systemd.network.networks = {
      "30-vm-inf" = {
        matchConfig.Name = "vm-inf";
        networkConfig = {
          ConfigureWithoutCarrier = true;
          LinkLocalAddressing = "no";
          IPv6AcceptRA = false;
        };
        linkConfig.RequiredForOnline = "no";
      };
      "40-vm-d-inf" = attach "vm-d-inf" "vm-inf";
      "40-vm-i-client" = attach "vm-i-client" "vm-inf";
      "40-vm-i-wan" = attach "vm-i-wan" "vm-wan";
      "30-vm-dev" = {
        matchConfig.Name = "vm-dev";
        networkConfig = {
          ConfigureWithoutCarrier = true;
          LinkLocalAddressing = "no";
          IPv6AcceptRA = false;
        };
        linkConfig.RequiredForOnline = "no";
      };
      "30-vm-wan" = {
        matchConfig.Name = "vm-wan";
        address = [ "172.31.255.1/29" ];
        networkConfig = {
          ConfigureWithoutCarrier = true;
          LinkLocalAddressing = "no";
          IPv6AcceptRA = false;
        };
        linkConfig.RequiredForOnline = "no";
      };
      "40-vm-d-tap" = attach "vm-d-tap" "vm-dev";
      "40-vm-g-lan" = attach "vm-g-lan" "vm-dev";
      "40-vm-g-wan" = attach "vm-g-wan" "vm-wan";
    };

    # The gateway implements policy. This second boundary prevents a compromised
    # gateway from reaching host services, private destinations, or other VMs.
    networking.nftables.tables = {
      vm-links = {
        family = "bridge";
        content = ''
          chain forward {
            type filter hook forward priority filter; policy accept;
            iifname "vm-d-inf" oifname "vm-i-client" ether type { ip, arp } accept
            iifname "vm-i-client" oifname "vm-d-inf" ether type { ip, arp } accept
            iifname { "vm-d-inf", "vm-i-client" } drop
            oifname { "vm-d-inf", "vm-i-client" } drop
            iifname "vm-d-tap" oifname "vm-g-lan" ether type { ip, arp } accept
            iifname "vm-g-lan" oifname "vm-d-tap" ether type { ip, arp } accept
            iifname { "vm-d-tap", "vm-g-lan" } drop
            oifname { "vm-d-tap", "vm-g-lan" } drop
          }
        '';
      };
      vm-boundary = {
        family = "inet";
        content = ''
          set blocked { type ipv4_addr; flags interval; elements = { ${blocked} }; }
          chain input {
            type filter hook input priority -10; policy accept;
            iifname { "vm-dev", "vm-inf", "vm-wan", "vm-d-tap", "vm-d-inf", "vm-i-client", "vm-i-wan", "vm-g-lan", "vm-g-wan" } counter drop
          }
          chain forward {
            type filter hook forward priority -10; policy accept;
            iifname "vm-inf" drop
            oifname "vm-inf" drop
            iifname "vm-dev" drop
            oifname "vm-dev" drop
            iifname "vm-wan" meta nfproto ipv6 drop
            oifname "vm-wan" meta nfproto ipv6 drop
            iifname "vm-wan" ip saddr != { 172.31.255.2, 172.31.255.6 } drop
            iifname "vm-wan" ip daddr @blocked counter drop
            iifname "vm-wan" oifname != "${cfg.uplink}" drop
            iifname "vm-wan" ct state invalid drop
            oifname "vm-wan" ct state established,related accept
            oifname "vm-wan" drop
          }
        '';
      };
      vm-uplink-nat = {
        family = "ip";
        content = ''
          chain postrouting {
            type nat hook postrouting priority srcnat; policy accept;
            oifname "${cfg.uplink}" ip saddr { 172.31.255.2, 172.31.255.6 } masquerade
          }
        '';
      };
    };
    networking.firewall.extraForwardRules = lib.mkIf config.networking.firewall.filterForward ''
      iifname "vm-wan" oifname "${cfg.uplink}" ip saddr { 172.31.255.2, 172.31.255.6 } accept
    '';

    systemd.services =
      (lib.mapAttrs' (name: machine: lib.nameValuePair "vm-${name}" (vmService name machine)) machines)
      // (lib.mapAttrs' (
        name: machine:
        lib.nameValuePair "vm-${name}-prepare" {
          description = "Provision persistent ${name} VM disks once";
          unitConfig.RequiresMountsFor = [ state ];
          serviceConfig = {
            Type = "oneshot";
            RemainAfterExit = true;
            ExecStart = "${prepare name machine}/bin/vm-${name}-prepare";
          };
        }
      ) machines);
  };
}
