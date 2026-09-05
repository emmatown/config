#!/usr/bin/env python3
"""Build and boot an isolated nested guest from this checkout; run inside vm-dev."""

import json
import pathlib
import socket
import subprocess
import tempfile
import time


def build(attribute):
    result = subprocess.check_output(
        ["nix", "build", attribute, "--no-link", "--json", "-L"], text=True
    )
    outputs = json.loads(result)[0]["outputs"]
    return pathlib.Path(next(iter(outputs.values())))


def ssh_ready(path):
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(2)
        connection.connect(str(path))
        connection.sendall(b"CONNECT 22\n")
        with connection.makefile("rb") as stream:
            if not stream.readline(128).startswith(b"OK "):
                return False
            return stream.readline(256).startswith(b"SSH-2.0-")


def main():
    image = build(".#packages.x86_64-linux.minivm-development-image")
    firmware = build(".#nixosConfigurations.minivm-development.pkgs.OVMF-cloud-hypervisor.fd")
    work = pathlib.Path(tempfile.mkdtemp(prefix="nested-", dir="/workspace"))
    print(f"Test artifacts retained in {work}", flush=True)

    # The outer Nix store is ext4. Stage the template on Btrfs once, then
    # exercise the same reflink operation the supervisor uses for instances.
    subprocess.run(["cp", "--sparse=always", str(image / "nixos.img"), str(work / "template.raw")], check=True)
    subprocess.run(["cp", "--reflink=always", str(work / "template.raw"), str(work / "guest.raw")], check=True)

    with (work / "boot.log").open("wb") as log:
        process = subprocess.Popen([
            "cloud-hypervisor",
            "--firmware", str(firmware / "FV/CLOUDHV.fd"),
            "--cpus", "boot=1,nested=off",
            "--memory", "size=1024M",
            "--disk", f"path={work / 'guest.raw'}",
            "--serial", "tty", "--console", "off",
            "--vsock", f"cid=3,socket={work / 'vsock'}",
            "--api-socket", str(work / "api.sock"),
        ], stdin=subprocess.DEVNULL, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 180
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError(f"Nested VMM exited; inspect {work / 'boot.log'}")
                try:
                    if ssh_ready(work / "vsock"):
                        break
                except OSError:
                    pass
                time.sleep(1)
            else:
                raise TimeoutError(f"Nested SSH did not start; inspect {work / 'boot.log'}")
            print("Nested NixOS guest booted and served SSH over vsock", flush=True)
            subprocess.run([
                "ch-remote", "--api-socket", str(work / "api.sock"), "power-button"
            ], check=True)
            process.wait(timeout=90)
            if process.returncode != 0:
                raise RuntimeError(f"Nested VMM exited with status {process.returncode}")
            print("Nested guest shut down cleanly")
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()


if __name__ == "__main__":
    main()
