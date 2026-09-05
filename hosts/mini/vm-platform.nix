{ inputs, pkgs, ... }:

let
  image = inputs.self.packages.x86_64-linux.minivm-development-image;
  firmware = pkgs.OVMF-cloud-hypervisor.firmware;
  revision = "dev-${builtins.hashString "sha256" "${image}:${firmware}"}";
in
{
  imports = [
    ../../modules/minivm
    ../../modules/minivm/workspace.nix
  ];

  services.vmWorkspace = {
    enable = true;
    image = inputs.self.packages.x86_64-linux.vm-workspace-image;
    gatewayImage = inputs.self.packages.x86_64-linux.vm-gateway-image;
    inferenceImage = inputs.self.packages.x86_64-linux.vm-inference-image;
  };

  services.minivm = {
    enable = true;
    controller.enable = true;
    # Leave room for the workspace, gateway, inference VM, host, and build jobs.
    controller.memoryBudgetMiB = 12288;
    templates.${revision} = {
      description = "Isolated mutable NixOS development guest";
      disk = "${image}/nixos.img";
      inherit firmware;
      mode = "development";
    };
  };
}
