{ config, lib, pkgs, ... }:

let
  cfg = config.services.gtd;
  port = config.my.ports.gtd;
  tailscale = config.services.tailscale.package;
in
{
  imports = [ ../../ports.nix ];

  options.services.gtd = {
    enable = lib.mkEnableOption "guided GTD web server (todo.txt)";

    dataDir = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/gtd";
      description = "Directory holding todo.txt, done.txt and timestamped backups.";
    };

    tailscaleServe = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Publish the server on the tailnet over HTTPS via `tailscale serve`.
        tailscale serve is NOT a declarative NixOS setting, so this runs the
        equivalent imperative command in a oneshot after tailscaled is up.
        It only succeeds once the node is logged in and HTTPS is enabled for
        the tailnet (Tailscale admin → DNS → HTTPS Certificates / MagicDNS).
        If the flags differ for your tailscale version, set this false and run
        `tailscale serve --bg http://127.0.0.1:${toString port}` by hand once.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.gtd = {
      isSystemUser = true;
      group = "gtd";
    };
    users.groups.gtd = { };

    systemd.services.gtd = {
      description = "Guided GTD web server (todo.txt)";
      after = [ "network.target" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        User = "gtd";
        Group = "gtd";
        ExecStart = "${lib.getExe' pkgs.gtd "gtd-server"} -addr 127.0.0.1:${toString port} -dir ${cfg.dataDir}";
        Restart = "on-failure";
        RestartSec = "5s";

        # State: StateDirectory creates and chowns /var/lib/gtd to the gtd user.
        # dataDir defaults to that path; keep them in sync if you override it.
        StateDirectory = "gtd";

        # Hardening — network-facing service handling only local files. The
        # process binds loopback and reads/writes the StateDirectory, which
        # systemd keeps writable even under ProtectSystem=strict.
        NoNewPrivileges = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectHome = true;
        ProtectSystem = "strict";
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        RemoveIPC = true;
        SystemCallArchitectures = "native";
      };
    };

    # tailscale serve is imperative; this best-effort oneshot applies it on boot.
    # It is gated to not block the boot transaction and simply fails (visibly,
    # via `systemctl status`) if the tailnet isn't ready — re-run by switching
    # again or invoking the command manually once.
    systemd.services.gtd-tailscale-serve = lib.mkIf cfg.tailscaleServe {
      description = "Publish gtd on the tailnet via tailscale serve";
      after = [ "tailscaled.service" "gtd.service" ];
      wants = [ "tailscaled.service" "gtd.service" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        Type = "oneshot";
        RemainAfterExit = true;
        ExecStart = "${tailscale}/bin/tailscale serve --bg http://127.0.0.1:${toString port}";
      };
    };
  };
}
