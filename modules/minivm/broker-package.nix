{ lib, buildGoModule }:

buildGoModule {
  pname = "vm-inference-broker";
  version = "0.1.0";
  src = lib.cleanSource ../../minivm;
  vendorHash = null;
  subPackages = [ "cmd/vm-inference-broker" ];
  env = {
    CGO_ENABLED = "0";
    GOPROXY = "off";
    GOTOOLCHAIN = "local";
  };
  checkPhase = ''
    runHook preCheck
    go test ./internal/broker
    runHook postCheck
  '';
  meta.description = "Credential-isolating Codex inference broker";
}
