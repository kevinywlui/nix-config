{ config, lib, pkgs, ... }:

let
  cfg = config.services.status-page;

  serviceChecks = lib.concatMapStringsSep "\n"
    (svc: ''
      STATE=$(${pkgs.systemd}/bin/systemctl is-active "${svc.unit}" 2>/dev/null || true)
      if [ "$STATE" = "active" ]; then CLS="ok"; LBL="active"
      else CLS="err"; LBL="${svc.name} ($STATE)"; fi
      SVCROWS="$SVCROWS<tr><td>${svc.name}</td><td class=\"$CLS\">$LBL</td></tr>"
    '')
    cfg.monitoredServices;

  generateScript = pkgs.writeShellScript "status-page-generate" ''
    set -euo pipefail
    OUT=/var/lib/status-page/index.html
    TMP=$(${pkgs.coreutils}/bin/mktemp /var/lib/status-page/.tmp.XXXXXX)
    trap '${pkgs.coreutils}/bin/rm -f "$TMP"' EXIT

    HOST=$(${pkgs.coreutils}/bin/cat /proc/sys/kernel/hostname)
    TS=$(${pkgs.coreutils}/bin/date '+%Y-%m-%d %H:%M:%S %Z')
    UPTIME=$(${pkgs.procps}/bin/uptime -p)
    read -r L1 L5 L15 _ < /proc/loadavg

    MEM_TOTAL=$(${pkgs.gawk}/bin/awk '/MemTotal/    {print $2}' /proc/meminfo)
    MEM_AVAIL=$(${pkgs.gawk}/bin/awk '/MemAvailable/{print $2}' /proc/meminfo)
    MEM_USED=$((MEM_TOTAL - MEM_AVAIL))
    MEM_PCT=$((MEM_USED * 100 / MEM_TOTAL))
    MEM_USED_H=$(${pkgs.gawk}/bin/awk "BEGIN {printf \"%.1f GiB\", $MEM_USED  / 1048576}")
    MEM_TOTAL_H=$(${pkgs.gawk}/bin/awk "BEGIN {printf \"%.1f GiB\", $MEM_TOTAL / 1048576}")

    DISK_USED=$(${pkgs.coreutils}/bin/df -h / | ${pkgs.gawk}/bin/awk 'NR==2{print $3}')
    DISK_TOTAL=$(${pkgs.coreutils}/bin/df -h / | ${pkgs.gawk}/bin/awk 'NR==2{print $2}')
    DISK_PCT=$(${pkgs.coreutils}/bin/df / | ${pkgs.gawk}/bin/awk 'NR==2{gsub(/%/,""); print $5}')

    mem_cls="bar"; [ "$MEM_PCT"  -ge 90 ] && mem_cls="bar crit" || { [ "$MEM_PCT"  -ge 70 ] && mem_cls="bar warn"; }; true
    disk_cls="bar"; [ "$DISK_PCT" -ge 90 ] && disk_cls="bar crit" || { [ "$DISK_PCT" -ge 70 ] && disk_cls="bar warn"; }; true

    SVCROWS=""
    ${serviceChecks}

    ${pkgs.coreutils}/bin/cat > "$TMP" <<HTMLEOF
    <!doctype html>
    <html lang="en">
    <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width,initial-scale=1">
    <meta http-equiv="refresh" content="60">
    <title>$HOST</title>
    <style>
    * { box-sizing: border-box; margin: 0; padding: 0; }
    body {
      font-family: ui-monospace, "Cascadia Code", monospace;
      background: #0f1117; color: #cdd6f4;
      padding: 2rem; max-width: 580px; margin: auto;
    }
    h1 { font-size: 1.25rem; margin-bottom: .2rem; }
    .ts { font-size: .8rem; color: #6c7086; margin-bottom: 2rem; }
    h2 {
      font-size: .75rem; text-transform: uppercase;
      letter-spacing: .1em; color: #6c7086;
      margin: 1.5rem 0 .6rem;
    }
    table { width: 100%; border-collapse: collapse; }
    td { padding: .3rem 0; font-size: .9rem; vertical-align: top; }
    td:first-child { color: #a6adc8; width: 38%; }
    .bar-wrap { background: #1e1e2e; border-radius: 3px; height: 5px; margin-top: 5px; }
    .bar      { background: #89b4fa; border-radius: 3px; height: 5px; }
    .bar.warn { background: #fab387; }
    .bar.crit { background: #f38ba8; }
    .ok  { color: #a6e3a1; }
    .err { color: #f38ba8; }
    </style>
    </head>
    <body>
    <h1>$HOST</h1>
    <div class="ts">Updated $TS &mdash; auto-refreshes every 60 s</div>

    <h2>System</h2>
    <table>
      <tr><td>Uptime</td><td>$UPTIME</td></tr>
      <tr><td>Load (1/5/15 m)</td><td>$L1 &nbsp; $L5 &nbsp; $L15</td></tr>
      <tr>
        <td>Memory</td>
        <td>$MEM_USED_H / $MEM_TOTAL_H ($MEM_PCT %)
          <div class="bar-wrap"><div class="$mem_cls" style="width:$MEM_PCT%"></div></div>
        </td>
      </tr>
      <tr>
        <td>Disk (/)</td>
        <td>$DISK_USED / $DISK_TOTAL ($DISK_PCT %)
          <div class="bar-wrap"><div class="$disk_cls" style="width:$DISK_PCT%"></div></div>
        </td>
      </tr>
    </table>

    <h2>Services</h2>
    <table>$SVCROWS</table>
    </body>
    </html>
    HTMLEOF

    ${pkgs.coreutils}/bin/mv "$TMP" "$OUT"
  '';
in
{
  imports = [ ../ports.nix ];

  options.services.status-page = {
    enable = lib.mkEnableOption "dynamic status page";

    monitoredServices = lib.mkOption {
      type = lib.types.listOf (lib.types.submodule {
        options = {
          name = lib.mkOption { type = lib.types.str; description = "Display name."; };
          unit = lib.mkOption { type = lib.types.str; description = "systemd unit name."; };
        };
      });
      default = [ ];
      description = "Systemd units to show health for.";
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.status-page = {
      isSystemUser = true;
      group = "status-page";
    };
    users.groups.status-page = { };

    systemd.tmpfiles.rules = [
      "d /var/lib/status-page 0750 status-page status-page -"
    ];

    systemd.services.status-page-generate = {
      description = "Generate status page HTML";
      serviceConfig = {
        Type = "oneshot";
        User = "status-page";
        ExecStart = generateScript;
        NoNewPrivileges = true;
        ProtectHome = true;
        PrivateTmp = true;
      };
    };

    systemd.timers.status-page-generate = {
      description = "Regenerate status page every minute";
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "10s";
        OnUnitActiveSec = "1m";
        Unit = "status-page-generate.service";
      };
    };

    systemd.services.status-page = {
      description = "Status Page Web Server";
      after = [ "network.target" "status-page-generate.service" ];
      wants = [ "status-page-generate.service" ];
      wantedBy = [ "multi-user.target" ];
      serviceConfig = {
        User = "status-page";
        ExecStart = "${pkgs.python3}/bin/python3 -m http.server --bind 127.0.0.1 --directory /var/lib/status-page ${toString config.my.ports.statusPage}";
        Restart = "on-failure";
        RestartSec = "10s";
        NoNewPrivileges = true;
        ProtectHome = true;
        PrivateTmp = true;
      };
    };
  };
}
