{ lib, buildGoModule, sqlite, role ? "controller" }:
let
  controller = role == "controller";
in
assert lib.assertMsg (builtins.elem role [ "controller" "supervisor" ]) "unknown minivm package role";
buildGoModule {
  pname = if controller then "minivm" else "minivm-supervisor";
  version = "0.1.0";
  src = lib.cleanSource ../../minivm;
  # No third-party Go modules. SQLite is supplied by pinned nixpkgs.
  vendorHash = null;
  subPackages = if controller then [
    "cmd/minivm"
    "cmd/minivm-controller"
  ] else [
    "cmd/minivm-supervisor"
    "cmd/minivm-vsock-proxy"
  ];
  env = {
    CGO_ENABLED = if controller then "1" else "0";
    GOPROXY = "off";
    GOTOOLCHAIN = "local";
  };
  buildInputs = lib.optional controller sqlite;
  checkPhase = ''
    runHook preCheck
    go test ${if controller then "./..." else "./internal/model ./internal/supervisor ./internal/vsock"}
    runHook postCheck
  '';
  meta.description = if controller then
    "Mini VM controller and CLI with system SQLite"
  else
    "Mini VM supervisor and vsock proxy using only the Go standard library";
}
