# Codex inference broker

The development guest runs Codex with a custom Responses provider and no OpenAI
credentials. A separate `vm-inference` guest owns the ChatGPT login and performs
authenticated upstream requests. It has 1 vCPU, 1 GiB RAM, and an 8 GiB persistent
root disk. No code from the development workspace runs inside it.

```text
vm-dev 192.168.126.2 --dedicated private bridge--> 192.168.126.1:8080 vm-inference
                                                            |
                                             fixed HTTPS upstream + OAuth
                                                            |
                                                          OpenAI
```

The dedicated client bridge has no host IP and only these two TAPs. Source-IP
validation is meaningful here because other guests cannot attach to that link.
The broker ignores forwarded identity headers. There is no bearer token or
ChatGPT credential in the dev guest. The link is HTTP inside the host's isolated
L2 segment; this trusts the host and is not suitable for cross-host transport.

The inference guest has its own uplink at `172.31.255.6/29`. Its WAN interface
accepts no new inbound connections. The broker permits public HTTPS, DNS, and NTP
egress; its HTTP client uses fixed OpenAI destinations and ignores proxy environment
variables. It does not route development traffic. The host independently blocks
private destinations and host services from this uplink.

## Protocol and limits

The broker supports the Responses protocol used by the Codex package from the
pinned `numtide/llm-agents.nix` input:

- `POST /v1/responses` forwards to the Codex subscription Responses endpoint,
  forcing streaming and `store=false`.
- `POST /v1/responses/compact` supports context compaction.
- Every other path, method, query string, and WebSocket upgrade is rejected.
- Caller authentication, cookies, account selectors, and forwarded headers are
  discarded. Only a small list of Codex correlation headers and the pinned
  Responses Lite protocol flag are relayed.
- The broker inserts its own access token and account ID. It discards upstream
  HTTP error bodies and credential-bearing headers, and redacts its known token
  strings in successful response bodies. It never logs prompts, tokens, or bodies.
- OAuth refresh uses the pinned CLI's public client ID and refresh protocol.
  Refresh is serialized, rotated tokens are written atomically with mode 0600,
  and an upstream 401 permits one refresh/retry. Failed refresh is throttled.
- Only stateless coding inference, local function/custom tools, and inline web
  search tools are supported. Account-history references, hosted file-search,
  remote MCP tools, and other unsupported request fields are rejected.
  The same tool restrictions apply to embedded Responses Lite and client tool
  search definitions.

The initial model and concurrency safeguard are in
`guests/minivm/inference-policy.nix`. The initial selection is `gpt-5.6-sol`, but
the broker accepts any syntactically valid model requested by Codex. There is no
VM-local request-count limit. Model availability and usage limits still depend on
the logged-in ChatGPT account.

The client uses the model catalog shipped by the Codex release; remote model
discovery and
ChatGPT account/plugin/cloud APIs are not exposed. SSE is enabled and WebSockets
are disabled for this first implementation. Client/backend upgrades require
rechecking these assumptions. Official custom-provider configuration does not
make this custom subscription broker an upstream-supported product.

## Deploy from your Mac

Use the workspace runbook's remote-build flow. The host configuration now includes
the inference image and private link. Before activation:

```sh
jj status
nix build --eval-store auto --store ssh-ng://emma@192.168.0.20 \
  .#packages.x86_64-linux.vm-inference-broker \
  .#checks.x86_64-linux.vm-network-policy --no-link -L

VM_SYSTEM=$(nix build --eval-store auto --store ssh-ng://emma@192.168.0.20 \
  .#nixosConfigurations.mini.config.system.build.toplevel \
  --no-link --print-out-paths -L)
ssh emma@192.168.0.20 "sudo '$VM_SYSTEM/bin/switch-to-configuration' test"
```

Existing workspace disks are deliberately retained. If the dev guest was already
provisioned, copy the updated checkout into it and run
`nixos-rebuild switch --flake .#vm-workspace` inside it. Its new NIC comes from the
host VM service, and its address/config come from that guest rebuild. The initial
Codex config is copied only if absent; the current template is available at
`/etc/codex/broker-config.toml`. Preserve any personal settings when updating an
existing `/root/.codex/config.toml`.

## Login only inside the inference VM

Add this entry to your Mac's SSH configuration:

```sshconfig
Host vm-inference
    HostName vm-inference
    User root
    ForwardAgent no
    ProxyCommand ssh emma@192.168.0.20 sudo -n /run/current-system/sw/bin/minivm-vsock-proxy --socket /run/vm-inference/vsock
```

Then run from your Mac:

```sh
ssh vm-inference 'systemctl stop vm-inference-broker'
ssh -t vm-inference \
  'sudo -u codex-broker env CODEX_HOME=/var/lib/codex-broker codex login --device-auth'
ssh vm-inference 'systemctl start vm-inference-broker'
```

Complete the displayed device-login flow in your browser. The login must complete
successfully before starting the broker. If device login is disabled for your
account, enable it in your account settings or use Codex's browser login with a
temporary SSH tunnel; do not copy the account credentials into the dev guest.

The broker and login CLI share `/var/lib/codex-broker/auth.json` **inside the
inference VM**. Keep the broker stopped while logging in/out, so two writers cannot
race token rotation. Neither the host nor dev guest mounts this directory.
No actual account login has been performed by the implementation agent.

## Live compatibility check

On `vm-dev`, after the login above:

```sh
test ! -e /root/.codex/auth.json
cd /workspace/emmatown-config
codex exec --skip-git-repo-check 'Reply with exactly: broker works'
codex exec --skip-git-repo-check \
  'Run pwd and go version using the shell tool, then report their output.'
test ! -e /root/.codex/auth.json
```

These requests consume the logged-in account's Codex usage. Also exercise a long
conversation and `/compact` in the interactive CLI, reconnect after a cancelled
stream, and repeat after token refresh. Inspect failures inside the broker VM with
`journalctl -u vm-inference-broker`; its logs exclude request and credential data.
The client receives deliberately generic upstream errors.

Stopping `vm-inference` on mini revokes inference immediately. Stopping only
`vm-gateway` blocks general dev internet access but leaves this explicitly granted
inference path available. Removing the login requires stopping the broker and
running `codex logout` as `codex-broker` with the same `CODEX_HOME` inside the
inference guest, then restarting it.

## Validation

Go tests use fake credentials and local upstreams to check header replacement,
stream redaction, endpoint/model/tool restrictions, refresh serialization and
rotation, redirects, errors, cancellation, concurrency, and durable budgets.
The NixOS network test checks the separate inference link and WAN isolation using
a fake listener. Neither substitutes for the real Codex/account compatibility
test above. Actual subscription inference remains unverified until that is run.

Protocol sources in the Codex source tree: `login/src/auth/manager.rs`,
`login/src/auth/storage.rs`, `model-provider-info/src/lib.rs`,
`core/src/client.rs`, and `codex-api/src/endpoint/compact.rs`.
See also [authentication](https://learn.chatgpt.com/docs/auth) and
[custom provider configuration](https://learn.chatgpt.com/docs/config-file/config-advanced).
