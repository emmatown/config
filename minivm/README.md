# Mini VM boot prototype

This is the first implementation slice, not the complete platform described in
[the design](../docs/vm-platform-design.md). Mini has been reinstalled with encrypted
Btrfs storage, and the original Go packages, firmware, and boot image have built
successfully on mini. Guest runtime validation is still pending.

For the isolated development VM and outbound gateway MVP, follow
[the workspace runbook](WORKSPACE.md). Those bootstrap VMs are host-managed so
development of the controller cannot take down its own workspace.

## Implemented source

- Go HTTP/JSON controller and CLI: template listing, create/list/inspect,
  start/stop/delete, and operation inspection.
- SQLite transactions, durable operation IDs, retained idempotency keys, resource
  revision checks, and memory admission including per-VMM overhead.
- A root-only-service supervisor reached through a local restricted Unix socket.
  Only the dedicated controller group and root can connect. Operations reference
  registered templates and UUIDs; clients cannot supply commands or disk paths.
- Per-instance Btrfs subvolumes and reflinked raw disks, separate VMM users,
  restricted systemd services, no guest NICs, and host-only SSH over vsock.
- Development NixOS image with its own EFI boot partition, persistent root/Nix
  store, and Cloud Hypervisor UEFI firmware.

The controller temporarily runs on the host, loopback-only, with a bootstrap
bearer token. This is a boot/lifecycle test harness. Moving it into its management
VM and implementing the policy/access/build VMs remain required before production
use. Do not expose the bootstrap API on the LAN or proxy it as a public service.

## Dependencies and build

There are no third-party Go modules. The controller uses the Go standard library
and a small binding to SQLite provided by the operating system (pinned nixpkgs in
production). The supervisor and vsock helper have no SQLite dependency and are
built separately with cgo disabled. Their dependency boundary is intentional.

From the repository root:

```sh
nix develop
go test -C minivm -race ./...
go vet -C minivm ./...
```

The development shell disables module downloads. Keep Go code in standard
`gofmt` style, with expanded control flow and blank lines between functions:

```sh
gofmt -w minivm/cmd minivm/internal
```

The flake exposes `packages.<system>.minivm` for the controller/CLI and
`packages.<system>.minivm-supervisor` for the privileged supervisor/vsock helper.
Both have `vendorHash = null`; no Cargo files or Go registry dependencies are
required. The controller needs a C compiler and SQLite development headers when
building outside the Nix development shell.

Local validation on macOS passed `go test -race ./...`, `go vet ./...`, and
builds of all four commands, including cgo-disabled supervisor/helper builds.
All Go files conform to `gofmt`. Both Nix package derivations evaluate successfully;
the original four Linux artifacts have also built successfully on mini. Actual
guest boots and the new workspace/gateway deployment remain untested.

On an x86_64 Linux Nix builder with KVM:

```sh
nix build .#packages.x86_64-linux.minivm-development-image -o result-dev-image
nix build .#packages.x86_64-linux.minivm-firmware -o result-firmware
nix build .#packages.x86_64-linux.minivm -o result-minivm
nix build .#packages.x86_64-linux.minivm-supervisor -o result-supervisor
```

The disk image is `nixos.img` (check the image output before creating the catalog),
and the firmware path is exposed by `pkgs.OVMF-cloud-hypervisor.firmware`.
Use immutable catalog revision IDs derived from build artifacts. Never change the
image/firmware behind an ID after creating instances.

## Host integration

Import `modules/minivm` and set `services.minivm` on the test host:

```nix
{
  services.minivm = {
    enable = true;
    templates.dev-v1 = {
      description = "Isolated mutable NixOS boot prototype";
      disk = /* immutable Nix store path to the raw image */;
      firmware = pkgs.OVMF-cloud-hypervisor.firmware;
      mode = "development";
    };
    controller.enable = true;
    controller.tokenFile = "/var/lib/vm-state-secrets/controller-token";
  };
}
```

Provision a random token of at least 32 characters at that path, root-readable
only, outside the Nix store. systemd passes it to the controller through
LoadCredential. The API listens only on 127.0.0.1:9080; tokens must not appear in
URLs. The CLI reads its token from `--token-file` or `MINIVM_TOKEN_FILE`.

`/var/lib/vm-state` and the Nix store containing the image must share a Btrfs
filesystem for reflinks. Separate subvolumes on the replacement layout do.
The module is disabled by default. Mini enables it through
`hosts/mini/vm-platform.nix`, with a template revision derived from the image and
firmware store paths. Provision the token before activating that configuration.

## API workflow

The contract for implemented endpoints is in `openapi.json`. All endpoints require
bootstrap bearer authentication. A POST creates a durable operation and returns
202 plus its Location; it does not claim guest readiness. Create provisions a
stopped machine. An explicit start launches its VMM.

```sh
minivm --token-file /path/to/token templates
minivm --token-file /path/to/token create example --template dev-v1 --key create-example-1
minivm --token-file /path/to/token operation OPERATION_UUID
minivm --token-file /path/to/token inspect MACHINE_UUID
minivm --token-file /path/to/token start MACHINE_UUID --revision 2 --key start-example-1
```

Use the revision returned by inspect, not a hard-coded number after retries.
Reuse the same idempotency key and payload after a lost response. Conflicting
payloads are rejected. Idempotency records currently remain indefinitely; names
remain reserved even after deletion, preventing accidental reuse.

Delete requires a stopped/failed record and retains the disk. It does not destroy
storage. Removing retained data is currently an explicit operator action outside
the API. Stop asks the guest to shut down through ACPI; it does not silently kill
a guest that fails to shut down. Runtime failures leave the operation pending for
safe replay and log a diagnostic on the host.

Machine state records the last completed operation; continuous reconciliation
with observed VMM/guest health and host-reboot autostart are not yet implemented.
A successful start means systemd accepted the VMM process, not that NixOS or SSH is
ready. Do not use this prototype for workloads requiring automatic recovery.

## Guest boot experiment

Inspect `journalctl -u minivm-MACHINE_UUID` and the instance's
`/var/lib/vm-state/instances/MACHINE_UUID/runtime/serial.log`. For authorized
operator-only SSH, use a ProxyCommand that runs the installed proxy as root on
the host:

```sh
ssh -o 'ProxyCommand=sudo minivm-vsock-proxy --socket /var/lib/vm-state/instances/MACHINE_UUID/runtime/vsock' root@MACHINE_UUID
```

The client still verifies the guest SSH host key and authenticates with a key
from this repository's authorized-keys.nix. Guest root has no password or serial
auto-login. Each VMM has its own vsock Unix socket; CID 3 is local to that isolated
Cloud Hypervisor backend, not an IP address or a shared host-vsock identity.

Copy the template source/locked dependencies needed for a rebuild over SSH; the
prototype deliberately has no internet interface. Verify distinct machine IDs
and SSH keys in two instances, a rebuild using cached inputs, reboot into the new
system, and persistence of files and the Nix database. Full online template
prototyping depends on the policy VM and controlled egress implementation.

## Next implementation work

1. Exercise the real supervisor under Linux; local tests use a command fake and
   do not establish Btrfs/KVM/systemd correctness on mini.
2. Boot two firmware guests and prove rebuild/rollback and fresh identities.
3. Complete Btrfs quota accounting and continuous host/guest state reconciliation.
4. Move the controller into its VM with an authenticated narrow supervisor channel.
5. Implement the policy VM and isolated links, DNS/IP/SNI enforcement, and VPN exits.
6. Implement the access VM, passkeys, SSH grants, Cloudflare DNS-01, and port routing.
7. Add API policy/publication endpoints, events, scoped credentials, and a build VM.

The workspace MVP adds a fixed public-egress gateway, separate from the dynamic
API. No managed-mode image, general policy API, hostname/SNI enforcement, VPN exit,
passkey service, certificate automation, or production API authorization is
implemented yet.
