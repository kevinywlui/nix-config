{ config, pkgs, dotfilesPath, ... }:

let
  basePath = "${dotfilesPath}/base/desktop";
  link = file: config.lib.file.mkOutOfStoreSymlink "${basePath}/${file}";
in
{
  # Display colour-temperature management (relay, dawn/dusk curve, bedtime
  # schedule, the nightlight scripts). See ../services/nightlight/README.md.
  imports = [ ../services/nightlight ];

  home.file = {
    ".local/bin/audio-switch.sh".source = link ".local/bin/audio-switch.sh";
    ".local/bin/eww-audio.sh".source = link ".local/bin/eww-audio.sh";
    ".local/bin/eww-workspaces.sh".source = link ".local/bin/eww-workspaces.sh";
    ".local/bin/eww-battery.sh".source = link ".local/bin/eww-battery.sh";
    ".local/bin/eww-output-watcher.sh".source = link ".local/bin/eww-output-watcher.sh";
  };

  xdg.configFile = {
    "sway".source = link ".config/sway";
    "hypr".source = link ".config/hypr";
    "kanshi".source = link ".config/kanshi";
    "kitty".source = link ".config/kitty";
    "dunst".source = link ".config/dunst";
    "gtk-3.0".source = link ".config/gtk-3.0";
    "gtk-4.0".source = link ".config/gtk-4.0";
    "rofi".source = link ".config/rofi";
    "eww".source = link ".config/eww";
    "psd/psd.conf" = {
      force = true;
      text = ''
        USE_OVERLAYFS="yes"
        USE_BACKUP="yes"
        BACKUP_LIMIT=5
      '';
    };
  };

  # Polkit authentication agent — the GUI "authenticate to continue" prompt.
  # Without a registered agent, polkit actions needing auth_self/auth_admin
  # (fprintd-enroll, udisks mounts, NetworkManager edits) fail with
  # PermissionDenied. The module's WantedBy=graphical-session.target is inert
  # here (the target is never activated — see ../services/nightlight); the unit
  # is started by name from sway's exec chain (base/desktop/.config/sway/config),
  # matching wl-gammarelay-rs et al.
  services.hyprpolkitagent.enable = true;

  systemd.user.services.kanshi = {
    Unit = {
      Description = "Kanshi dynamic display configuration";
      After = [ "graphical-session.target" ];
      PartOf = [ "graphical-session.target" ];
    };
    Service = {
      ExecStart = "${pkgs.kanshi}/bin/kanshi";
      Restart = "on-failure";
      RestartSec = "1s";
    };
    Install = {
      WantedBy = [ "graphical-session.target" ];
    };
  };

  systemd.user.services.chrome-prewarm = {
    Unit = {
      Description = "Chrome prewarm (background, no window)";
      After = [ "psd.service" "graphical-session.target" ];
      Wants = [ "psd.service" ];
    };
    Service = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "chrome-prewarm" ''
        ${pkgs.google-chrome}/bin/google-chrome-stable --no-startup-window &
      '';
    };
    Install = {
      WantedBy = [ "graphical-session.target" ];
    };
  };

  systemd.user.services.steam-silent = {
    Unit = {
      Description = "Steam (silent startup)";
      After = [ "graphical-session.target" ];
      Wants = [ "graphical-session.target" ];
    };
    Service = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "steam-silent" ''
        ${pkgs.steam}/bin/steam -silent &
      '';
    };
    Install = {
      WantedBy = [ "graphical-session.target" ];
    };
  };

  systemd.user.services.check-nix-update = {
    Unit = {
      Description = "Check if NixOS configuration is stale";
      After = [ "graphical-session.target" ];
    };
    Service = {
      Type = "oneshot";
      ExecStart = "${pkgs.writeShellScript "check-update" ''
        if [ $(($(${pkgs.coreutils}/bin/date +%s) - $(${pkgs.coreutils}/bin/stat -c %Y /run/current-system))) -gt 2592000 ]; then
          ${pkgs.libnotify}/bin/notify-send "System Update" "Your running system is over 30 days old. Consider running nh os switch." -u normal
        fi
      ''}";
    };
  };

  systemd.user.timers.check-nix-update = {
    Unit = {
      Description = "Periodically check for NixOS updates";
    };
    Timer = {
      OnBootSec = "15m";
      OnUnitActiveSec = "1d";
    };
    Install = {
      WantedBy = [ "timers.target" ];
    };
  };
}
