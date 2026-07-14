{ lib, pkgs, ... }:

{
  security.tpm2.enable = true;

  services.openssh = {
    # ssh-tpm-agent generates TPM-wrapped host keys instead. Do not generate
    # the default unprotected host private keys in /etc/ssh.
    generateHostKeys = false;
    hostKeys = [
      {
        type = "ecdsa";
        path = "/etc/ssh/ssh_tpm_host_ecdsa_key.pub";
      }
      {
        type = "rsa";
        path = "/etc/ssh/ssh_tpm_host_rsa_key.pub";
      }
    ];
    settings.HostKeyAgent = "/var/tmp/ssh-tpm-agent.sock";
  };

  # ssh-tpm-agent's upstream system units, expressed declaratively for NixOS.
  # The .tpm files are TPM-wrapped key blobs; signing remains bound to this TPM.
  systemd = {
    sockets.ssh-tpm-agent = {
      description = "SSH TPM agent socket";
      wantedBy = [ "sockets.target" ];
      socketConfig = {
        ListenStream = "/var/tmp/ssh-tpm-agent.sock";
        SocketMode = "0600";
      };
    };

    services = {
      ssh-tpm-genkeys = {
        description = "Generate TPM-backed SSH host keys";
        unitConfig.ConditionPathExists = [
          "|!/etc/ssh/ssh_tpm_host_ecdsa_key.tpm"
          "|!/etc/ssh/ssh_tpm_host_ecdsa_key.pub"
          "|!/etc/ssh/ssh_tpm_host_rsa_key.tpm"
          "|!/etc/ssh/ssh_tpm_host_rsa_key.pub"
        ];
        serviceConfig = {
          Type = "oneshot";
          RemainAfterExit = true;
          ExecStart = "${lib.getExe' pkgs.ssh-tpm-agent "ssh-tpm-keygen"} -A";
        };
      };

      ssh-tpm-agent = {
        description = "SSH TPM host-key agent";
        requires = [ "ssh-tpm-agent.socket" ];
        wants = [ "ssh-tpm-genkeys.service" ];
        after = [ "ssh-tpm-genkeys.service" ];
        serviceConfig = {
          ExecStart = "${lib.getExe pkgs.ssh-tpm-agent} --key-dir /etc/ssh";
          Restart = "always";
        };
      };

      sshd = {
        requires = [
          "ssh-tpm-agent.socket"
          "ssh-tpm-genkeys.service"
        ];
        after = [ "ssh-tpm-genkeys.service" ];
      };
    };
  };
}
