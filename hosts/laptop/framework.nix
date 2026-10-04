{ pkgs, ... }:

{
  services.fwupd.enable = true;
  services.fprintd.enable = true;

  hardware = {
    acpilight.enable = true;
    sensor.iio.enable = true;
    graphics = {
      extraPackages = with pkgs; [
        intel-media-driver
        intel-compute-runtime
        vpl-gpu-rt
      ];
      extraPackages32 = with pkgs.driversi686Linux; [
        intel-media-driver
      ];
    };
  };

  boot = {
    initrd.kernelModules = [ "xe" ];
    kernelParams = [ "nvme.noacpi=1" ];
    blacklistedKernelModules = [ "cros-usbpd-charger" ];
    extraModulePackages = [ ];
    kernelModules = [
      "cros_ec"
      "cros_ec_lpcs"
    ];
  };

  environment.systemPackages = with pkgs; [
    framework-tool
    fw-ectool
  ];
}
