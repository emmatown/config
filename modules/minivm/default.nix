{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.minivm;
  catalog = pkgs.writeText "minivm-catalog.json" (builtins.toJSON cfg.templates);
  runtimePath = with pkgs; [
    btrfs-progs
    coreutils
    shadow
    systemd
    cloud-hypervisor
  ];
in
{
  options.services.minivm = {
    enable = lib.mkEnableOption "the isolated minivm boot prototype";
    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { };
    };
    supervisorPackage = lib.mkOption {
      type = lib.types.package;
      default = pkgs.callPackage ./package.nix { role = "supervisor"; };
    };
    templates = lib.mkOption {
      type = lib.types.attrsOf (
        lib.types.submodule {
          options = {
            description = lib.mkOption { type = lib.types.str; };
            disk = lib.mkOption { type = lib.types.path; };
            firmware = lib.mkOption { type = lib.types.path; };
            mode = lib.mkOption {
              type = lib.types.enum [ "development" ];
              default = "development";
            };
          };
        }
      );
      default = { };
    };
    controller.enable = lib.mkEnableOption "the loopback-only bootstrap controller";
    controller.memoryBudgetMiB = lib.mkOption {
      type = lib.types.ints.between 512 20480;
      default = 20480;
      description = "Memory admission budget including per-VMM overhead.";
    };
    controller.tokenFile = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/vm-state-secrets/controller-token";
    };
  };
  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = pkgs.stdenv.hostPlatform.system == "x86_64-linux";
        message = "minivm prototype requires x86_64 Linux";
      }
    ];
    boot.kernelModules = [ "kvm-intel" ];
    users.groups.minivm-control = { };
    users.users.minivm-controller = {
      isSystemUser = true;
      group = "minivm-control";
    };
    environment.systemPackages = [
      cfg.package
      cfg.supervisorPackage
      pkgs.cloud-hypervisor
      pkgs.btrfs-progs
    ];
    environment.etc."minivm/catalog.json".source = catalog;
    systemd.tmpfiles.rules = [
      "d /var/lib/vm-state 0755 root root -"
      "d /var/lib/vm-state/instances 0711 root root -"
      "d /var/lib/vm-state/manifests 0700 root root -"
      "d /var/lib/vm-state/receipts 0700 root root -"
      "d /var/lib/vm-state/retained 0700 root root -"
      "d /var/lib/vm-state/controller 0700 minivm-controller minivm-control -"
    ];
    systemd.services.minivm-supervisor = {
      description = "Mini VM local privileged supervisor";
      wantedBy = [ "multi-user.target" ];
      unitConfig.RequiresMountsFor = [ "/var/lib/vm-state" ];
      path = runtimePath;
      serviceConfig = {
        ExecStart = "${cfg.supervisorPackage}/bin/minivm-supervisor --socket /run/minivm-supervisor/api.sock --catalog ${catalog}";
        RuntimeDirectory = "minivm-supervisor";
        RuntimeDirectoryMode = "0750";
        Group = "minivm-control";
        UMask = "0077";
        Restart = "on-failure";
      };
    };
    systemd.services.minivm-controller = lib.mkIf cfg.controller.enable {
      description = "Mini VM loopback bootstrap API (temporary host placement)";
      wantedBy = [ "multi-user.target" ];
      after = [ "minivm-supervisor.service" ];
      serviceConfig = {
        User = "minivm-controller";
        Group = "minivm-control";
        LoadCredential = "token:${cfg.controller.tokenFile}";
        ExecStart = "${cfg.package}/bin/minivm-controller --database /var/lib/vm-state/controller/state.db --catalog ${catalog} --token-file %d/token --supervisor-socket /run/minivm-supervisor/api.sock --memory-budget-mib ${toString cfg.controller.memoryBudgetMiB}";
        Restart = "on-failure";
        UMask = "0077";
        ProtectSystem = "strict";
        ProtectHome = true;
        ReadWritePaths = [ "/var/lib/vm-state/controller" ];
        NoNewPrivileges = true;
        PrivateTmp = true;
      };
    };
  };
}
