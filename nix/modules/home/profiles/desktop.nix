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
    ".local/bin/eww-sun.sh".source = link ".local/bin/eww-sun.sh";
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
  # PermissionDenied. The module's WantedBy=graphical-session.target starts it:
  # uwsm activates that target (see nix/modules/nixos/profiles/desktop.nix), so
  # no explicit start line is needed — same as wl-gammarelay-rs and kanshi.
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

  # ---------------------------------------------------------------------------
  # eww — status bar + background clock (see base/desktop/.config/eww/)
  # ---------------------------------------------------------------------------
  #
  # Split into two bound units so a daemon segfault self-heals. eww SIGSEGVs
  # periodically (an upstream crash, not a config fault). Without this split, a
  # daemon crash goes unnoticed by the output-watcher — which only re-opens
  # windows on a sway `output` event — so the bar stays gone until the next
  # monitor hotplug or sway reload. With it:
  #
  #   eww-daemon : runs `eww daemon` in the foreground under systemd. A crash
  #                trips Restart=on-failure; Upholds= then re-pulls the watcher.
  #   eww-bars   : the output-watcher. BindsTo + After the daemon, so it is torn
  #                down with a crashing daemon; the daemon's Upholds= restarts it
  #                against the fresh daemon, where it re-opens bar + clock.
  #
  # Neither sets Environment: both inherit the user-manager env, where
  # `uwsm finalize` has exported SWAYSOCK + WAYLAND_DISPLAY and the nix profiles
  # are on PATH — which the daemon's deflisten/defpoll children (swaymsg, jq,
  # pamixer, …) need. graphical-session.target is held by uwsm's waitenv gate
  # until that env exists, so ordering After= it is the readiness gate. This is
  # the same pure-systemd pattern as kanshi / wl-gammarelay-rs above; the sway
  # `exec_always` launch line was retired (see base/desktop/.config/sway/config).
  systemd.user.services.eww-daemon = {
    Unit = {
      Description = "eww daemon (status bar + background clock)";
      After = [ "graphical-session.target" ];
      PartOf = [ "graphical-session.target" ];
      # Keep the window-placement watcher alive whenever the daemon is up: after
      # a crash-respawn the bar/clock windows are gone until the watcher re-runs,
      # and Upholds= restarts an inactive-or-failed eww-bars with no delay.
      Upholds = [ "eww-bars.service" ];
    };
    Service = {
      # --no-daemonize keeps the daemon in the foreground so systemd tracks it as
      # the main process and Restart=on-failure can catch the segfault.
      ExecStart = "${pkgs.eww}/bin/eww daemon --no-daemonize";
      Restart = "on-failure";
      RestartSec = "1s";
    };
    Install = {
      WantedBy = [ "graphical-session.target" ];
    };
  };

  systemd.user.services.eww-bars = {
    Unit = {
      Description = "eww bar/clock window-placement watcher";
      # BindsTo (not just Requires) + After: the watcher must die with a daemon
      # that exits *unexpectedly*, so a fresh daemon comes up with no orphaned
      # watcher; the daemon's Upholds= then brings it back once active again.
      # No Install/WantedBy: eww-bars is pulled solely by the daemon's Upholds=,
      # which governs both first start and post-crash restart identically.
      BindsTo = [ "eww-daemon.service" ];
      After = [ "eww-daemon.service" ];
    };
    Service = {
      # The script's own `eww daemon` + ping-wait is a no-op here (the daemon
      # service already owns it) but keeps it working under bare Stow.
      ExecStart = "%h/.local/bin/eww-output-watcher.sh";
    };
  };

  systemd.user.services.chrome-prewarm = {
    Unit = {
      Description = "Chrome prewarm (background, no window)";
      After = [ "psd.service" "graphical-session.target" ];
      Wants = [ "psd.service" ];
    };
    Service = {
      # Type=simple (not oneshot) with the process in the foreground (no `&`)
      # so systemd tracks the warmed Chrome rather than orphaning it.
      Type = "simple";
      ExecStart = pkgs.writeShellScript "chrome-prewarm" ''
        ${pkgs.google-chrome}/bin/google-chrome-stable --no-startup-window
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
      # Type=simple (not oneshot) with the process in the foreground (no `&`)
      # so systemd tracks the Steam tray rather than orphaning it.
      Type = "simple";
      ExecStart = pkgs.writeShellScript "steam-silent" ''
        ${pkgs.steam}/bin/steam -silent
      '';
    };
    Install = {
      WantedBy = [ "graphical-session.target" ];
    };
  };

  systemd.user.services.check-nix-update = {
    Unit = {
      Description = "Check if NixOS configuration or flake inputs are stale";
      After = [ "graphical-session.target" ];
    };
    Service = {
      Type = "oneshot";
      ExecStart = "${pkgs.writeShellScript "check-update" ''
        # Stale running system: built more than 30 days ago → time to switch.
        if [ $(($(${pkgs.coreutils}/bin/date +%s) - $(${pkgs.coreutils}/bin/stat -c %Y /run/current-system))) -gt 2592000 ]; then
          ${pkgs.libnotify}/bin/notify-send "System Update" "Your running system is over 30 days old. Consider running nh os switch." -u normal
        fi
        # Stale flake inputs: newest locked input more than 30 days old → time to
        # run /update-flake. lastModified (vs the file mtime) survives checkouts.
        lock="${dotfilesPath}/flake.lock"
        if [ -r "$lock" ]; then
          newest=$(${pkgs.jq}/bin/jq '[.nodes[].locked.lastModified // empty] | max // 0' "$lock")
          if [ "$newest" -gt 0 ] && [ $(($(${pkgs.coreutils}/bin/date +%s) - newest)) -gt 2592000 ]; then
            ${pkgs.libnotify}/bin/notify-send "Flake inputs stale" "Newest flake input is over 30 days old. Run /update-flake to review updates." -u normal
          fi
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
