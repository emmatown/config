#!/usr/bin/env python3
"""Run inside the development guest before using it as a workspace."""

import fcntl
import pathlib
import socket
import subprocess
import urllib.request


def blocked(address, port):
    try:
        with socket.create_connection((address, port), timeout=3):
            raise AssertionError(f"Unexpected access to {address}:{port}")
    except (TimeoutError, ConnectionError, OSError):
        print(f"Blocked: {address}:{port}")


def main():
    with open("/dev/kvm", "rb") as kvm:
        assert fcntl.ioctl(kvm, 0xAE00, 0) == 12, "Unexpected KVM API version"
    print("Nested KVM device works")

    fs = subprocess.check_output(
        ["findmnt", "-n", "-o", "FSTYPE", "--target", "/workspace"], text=True
    ).strip()
    assert fs == "btrfs", f"Expected Btrfs workspace, got {fs}"
    assert pathlib.Path("/etc/machine-id").read_text().strip()

    with urllib.request.urlopen("https://cache.nixos.org/nix-cache-info", timeout=20) as response:
        assert response.status == 200
    print("DNS and public HTTPS work")

    for address, port in [
        ("emma-mini.local", 22),
        ("192.168.0.1", 80),
        ("172.31.255.1", 22),
        ("172.31.255.1", 9080),
        ("192.168.127.1", 22),
        ("169.254.169.254", 80),
    ]:
        blocked(address, port)

    addresses = subprocess.check_output(["ip", "-6", "addr", "show", "scope", "global"], text=True)
    assert "inet6" not in addresses, "Unexpected IPv6 address"
    print("Workspace checks passed; also run the gateway-down test in the runbook")


if __name__ == "__main__":
    main()
