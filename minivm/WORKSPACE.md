# Isolated development workspace

This MVP adds host-managed Cloud Hypervisor services, independent of the
controller being developed. `vm-dev` has 4 vCPUs, 8 GiB RAM, a 64 GiB persistent
ext4 root disk, and a 64 GiB Btrfs disk at `/workspace`. `vm-gateway` has 1 vCPU,
1 GiB RAM, and an 8 GiB root disk. The development VMM has no host-side memory
or swap cap, so test workloads cannot trigger a systemd cgroup OOM. The gateway
and inference VMMs remain limited to their guest memory plus 2 GiB of overhead.
The controller's separate admission budget is reduced to 12 GiB.

The development image includes Codex from the pinned `numtide/llm-agents.nix`
flake input. It is configured for full access without approval prompts and to use a
separate inference VM. ChatGPT credentials are never provisioned into the dev
guest. Follow [the inference runbook](INFERENCE.md) for broker login and testing.
The inference VM adds 1 vCPU, 1 GiB RAM, and an 8 GiB persistent root disk.
Do not run `codex login` in the development guest.

The services start at host boot and retain their disks under
`/var/lib/vm-state/bootstrap`. Rebuilding the host does not replace existing
guest disks. Stop a workspace with `sudo systemctl stop vm-dev`; start it with
`sudo systemctl start vm-dev`. A failed graceful shutdown is terminated after
the service's stop timeout. No host directory, SSH agent, controller credential,
or supervisor socket is shared into either guest.

## Network boundary

```text
Mac --SSH to mini--> root-only vsock proxy --> vm-dev SSH

vm-dev 192.168.127.2
  | private L2 bridge, no host IP
vm-gateway 192.168.127.1 / 172.31.255.2
  | DNS and public IPv4 forwarding + NAT
mini 172.31.255.1
  | independent private-address/host-access block + NAT
eno1 --> public internet
```

IPv6 is disabled in the guests. The private development bridge drops non-IP/ARP
traffic, including VLAN-tagged frames and IPv6. Only DNS is exposed by the gateway
to the development guest. IP SSH access to either guest is closed; SSH uses vsock.
There are no LAN ingress forwards. A stopped gateway provides no alternate egress.

The host keeps the TAP devices and minimal uplink routing. The VMM users own only
their TAP devices and disks, have no capabilities, and have IP socket access
denied by systemd. Host nftables also blocks private destinations and host input
from the gateway, even if the gateway guest changes its firewall. This MVP is one
development segment, not a shared network for arbitrary workload VMs.

The address ranges are fixed for this bootstrap setup. They must not overlap your
host routes. The uplink is mini's `eno1`. This implements public IPv4 egress, not
hostname/SNI policies, VPN routing, HTTPS publication, or passkey access.

## Build from your Mac

Run from the local repository in Terminal, where SSH to mini works:

```sh
jj status
export SSH_SK_PROVIDER=/usr/lib/ssh-keychain.dylib
SSH_ASKPASS_REQUIRE=force SSH_ASKPASS=true /usr/bin/ssh-add -K

# This runs five Linux test VMs against the actual IP policies, using a fake
# public server and DNS server. It does not need public internet during the test.
nix build --eval-store auto --store ssh-ng://emma@192.168.0.20 \
  .#checks.x86_64-linux.vm-network-policy --no-link -L

# Continue only after the policy test succeeds.
VM_SYSTEM=$(nix build --eval-store auto --store ssh-ng://emma@192.168.0.20 \
  .#nixosConfigurations.mini.config.system.build.toplevel \
  --no-link --print-out-paths -L)
```

The initial workspace image is larger than the original boot-test image. All
outputs stay on mini. Nix does not need a remote checkout.

Provision the bootstrap controller token if it does not already exist:

```sh
ssh emma@192.168.0.20 'sudo sh -s' <<'EOF'
set -eu
install -d -m 0700 /var/lib/vm-state-secrets
if [ ! -e /var/lib/vm-state-secrets/controller-token ]; then
  umask 077
  head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n' \
    > /var/lib/vm-state-secrets/controller-token
fi
EOF

ssh emma@192.168.0.20 'cat /sys/module/kvm_intel/parameters/nested'
```

Nested KVM must report `Y` or `1`. The configuration sets `nested=1` for subsequent
module loads. If mini currently reports `N`, use the persistent activation commands
at the end of this runbook and reboot before testing; do not unload a KVM module
with active VMs.

Otherwise activate temporarily first, then inspect services and boot logs:

```sh
ssh emma@192.168.0.20 "sudo '$VM_SYSTEM/bin/switch-to-configuration' test"
ssh emma@192.168.0.20 \
  'sudo systemctl --no-pager --full status vm-dev vm-gateway vm-inference'
ssh emma@192.168.0.20 \
  'sudo journalctl -b -u vm-dev -u vm-gateway -u vm-inference --no-pager -n 100'
```

## SSH and copy the checkout

Add this host entry to your Mac's `~/.ssh/config`:

```sshconfig
Host vm-dev
    HostName vm-dev
    User root
    ForwardAgent no
    ProxyCommand ssh emma@192.168.0.20 sudo -n /run/current-system/sw/bin/minivm-vsock-proxy --socket /run/vm-dev/vsock
```

Then `ssh vm-dev`. Your Mac authenticates to both SSH servers with its own key.
The guest generates its own SSH host keys on its first boot; SSH records and
checks that identity on later connections.

Copy the current tracked files, including uncommitted edits, from your Mac:

```sh
ssh vm-dev 'mkdir -p /workspace/emmatown-config'
jj file list | tar -cf - -T - | \
  ssh vm-dev 'tar -xf - -C /workspace/emmatown-config'
```

This creates a source copy without local VCS metadata. Inside the guest, run
`jj git init --colocate /workspace/emmatown-config` to begin tracking it. For full
history you can instead use `jj git clone` with your repository URL; private
remote authentication must be provisioned separately, not via agent forwarding.

## Acceptance checks inside the guest

```sh
cd /workspace/emmatown-config
python3 minivm/scripts/check-workspace.py
nix develop --command go test -C minivm -race ./...
nix develop --command go vet -C minivm ./...
python3 minivm/scripts/nested-smoke.py
```

The nested smoke test builds the current checkout's basic NixOS guest image,
stages it on Btrfs, reflinks a writable instance, boots Cloud Hypervisor with no
NIC, waits for an SSH banner over vsock, and requests graceful shutdown. It fails
if KVM/boot/SSH/shutdown does not work. It retains its directory and boot log under
`/workspace/nested-*` for inspection. It does not yet test all controller lifecycle
operations or authenticate an SSH session to the nested guest.

The outer root and `/nix` are ext4. When testing the supervisor inside this VM,
stage template disks under `/workspace/templates` and use a supervisor state
directory under `/workspace` so source and clone share Btrfs.

To prove persistent identity and data, inside the guest:

```sh
cat /etc/machine-id > /workspace/expected-machine-id
ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub > /workspace/expected-host-key
printf 'persistent workspace\n' > /workspace/persistence-check
nixos-rebuild build --flake .#vm-workspace
nixos-rebuild switch --flake .#vm-workspace
reboot
```

Reconnect, compare the recorded values, and inspect the file. Also exercise
`nixos-rebuild switch --rollback` and reconnect after another reboot. Guest
configuration changes affect its own disks; they do not change mini's host policy.

From the Mac, stop `vm-gateway` on mini. Guest public HTTP requests must fail,
while SSH to `vm-dev` must still work. Start the gateway again and verify egress
recovers:

```sh
ssh emma@192.168.0.20 'sudo systemctl stop vm-gateway'
ssh vm-dev 'curl --max-time 5 https://cache.nixos.org/nix-cache-info'
ssh emma@192.168.0.20 'sudo systemctl start vm-gateway'
```

After these checks pass, persist the host configuration using the `VM_SYSTEM`
value from the build, then reboot mini and repeat the persistence checks:

```sh
ssh emma@192.168.0.20 "sudo nix-env --profile /nix/var/nix/profiles/system --set '$VM_SYSTEM'"
ssh emma@192.168.0.20 "sudo '$VM_SYSTEM/bin/switch-to-configuration' switch"
```

Setting the system profile records the generation for subsequent rebuilds and
boot selection; invoking the activation script alone does not update that profile.

## Validation status

The source and Nix derivations can be checked on the Mac. The network test,
Cloud Hypervisor TAP attachment, nested KVM, guest boot/rebuild/rollback, and host
reboot recovery require execution on mini. Until those checks pass, this is an
implemented MVP awaiting runtime validation, not a verified isolation deployment.
