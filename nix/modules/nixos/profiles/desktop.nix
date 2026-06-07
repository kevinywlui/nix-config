{ config, lib, pkgs, ... }:

{
  programs.sway = {
    enable = true;
    wrapperFeatures.gtk = true;
  };

  programs.uwsm = {
    enable = true;
    waylandCompositors.sway = {
      prettyName = "Sway";
      comment = "Sway compositor managed by UWSM";
      binPath = "/run/current-system/sw/bin/sway";
    };
  };

  xdg.portal = {
    enable = true;
    extraPortals = with pkgs; [
      xdg-desktop-portal-wlr
      xdg-desktop-portal-gtk
    ];
    config.sway = {
      default = lib.mkForce [ "wlr" "gtk" ];
    };
  };

  security.pam.services.hyprlock.text = ''
    auth sufficient ${pkgs.linux-pam}/lib/security/pam_unix.so nullok try_first_pass
    auth required ${pkgs.linux-pam}/lib/security/pam_deny.so
    account required ${pkgs.linux-pam}/lib/security/pam_permit.so
  '';

  security.rtkit.enable = true;
  hardware.firmware = [ pkgs.linux-firmware ];

  hardware.bluetooth = {
    enable = true;
    powerOnBoot = true;
  };
  services.blueman.enable = true;
  services.pipewire = {
    enable = true;
    alsa.enable = true;
    alsa.support32Bit = true;
    pulse.enable = true;
  };

  services.pipewire.wireplumber.extraConfig = {
    "50-audio-policy" = {
      "monitor.alsa.rules" = [
        # Auto-profile: built-in and GoMic always enumerate their sinks.
        # Generic USB (dock DACs etc.) also gets auto-profile.
        # KT USB is explicitly excluded — must stay on iec958-stereo (S/PDIF output).
        {
          matches = [{ "device.name" = "alsa_card.pci-0000_00_1f.3"; }];
          actions = { "update-props" = { "api.acp.auto-profile" = true; }; };
        }
        {
          matches = [{ "device.name" = "~alsa_card.usb-*"; }];
          actions = { "update-props" = { "api.acp.auto-profile" = true; }; };
        }
        {
          matches = [{ "device.name" = "~alsa_card.usb-KTMicro*"; }];
          actions = { "update-props" = { "api.acp.auto-profile" = false; }; };
        }
        # Sink priorities: BT (bluez default) > Samson (1000) > dock/unknown USB (750)
        #                  > KT USB desktop speakers (500) > built-in (100)
        {
          matches = [{ "node.name" = "~alsa_output.pci-*"; }];
          actions = { "update-props" = { "priority.session" = 100; }; };
        }
        {
          matches = [{ "node.name" = "~alsa_output.usb-KTMicro*"; }];
          actions = { "update-props" = { "priority.session" = 500; }; };
        }
        {
          matches = [{ "node.name" = "~alsa_output.usb-*"; }];
          actions = { "update-props" = { "priority.session" = 750; }; };
        }
        {
          matches = [{ "node.name" = "~alsa_output.usb-Samson*"; }];
          actions = { "update-props" = { "priority.session" = 1000; }; };
        }
      ];
    };
  };

  users.users.klui.extraGroups = [ "video" ];

  hardware.graphics = {
    enable = true;
    extraPackages = with pkgs; [
      intel-media-driver
      vpl-gpu-rt
    ];
  };

  services.psd.enable = true;

  security.sudo.extraConfig = ''
    klui ALL=(ALL) NOPASSWD: ${pkgs.profile-sync-daemon}/bin/psd-overlay-helper
  '';

  programs.steam.enable = true;

  services.greetd = {
    enable = true;
    settings = {
      default_session = {
        command = "uwsm start -F -- /run/current-system/sw/bin/sway";
        user = "greeter";
      };
      initial_session = {
        command = "uwsm start -F -- /run/current-system/sw/bin/sway";
        user = "klui";
      };
    };
  };

  security.pam.services.greetd.fprintAuth = true;

  # uwsm launches the compositor as a systemd unit that does not inherit
  # home-manager's hm-session-vars.sh, so cursor env must be system-wide here
  # for `uwsm finalize XCURSOR_*` (sway config) to export real values.
  environment.sessionVariables = {
    XCURSOR_THEME = "Adwaita";
    XCURSOR_SIZE = "32";
  };

  environment.systemPackages =
    let
      desktopTools = with pkgs; [
        google-chrome
        blueman
        dunst
        udiskie
        libnotify
        networkmanagerapplet
        rofi
      ];

      waylandTools = with pkgs; [
        hyprlock
        swayidle
        swaybg
        eww
        jq
        kanshi
        nwg-displays
        grim
        slurp
        wl-clipboard
        gammastep
        wl-gammarelay-rs
      ];

      audioTools = with pkgs; [
        pamixer
        pavucontrol
        playerctl
      ];

      guiUtils = with pkgs; [
        calibre
        claude-desktop # unofficial Tier 3 input; see flake.nix + AGENTS.md
        kitty
        obsidian
        adwaita-icon-theme
        papirus-icon-theme
        telegram-desktop
      ];
    in
    desktopTools ++ waylandTools ++ audioTools ++ guiUtils;

  fonts = {
    packages = with pkgs; [
      nerd-fonts.meslo-lg
      nerd-fonts.fira-code
      noto-fonts
      noto-fonts-cjk-sans
      noto-fonts-color-emoji
      inter
    ];
    fontconfig = {
      enable = true;
      defaultFonts = {
        monospace = [ "MesloLGS Nerd Font" "FiraCode Nerd Font" ];
        sansSerif = [ "Inter" "Noto Sans" ];
        serif = [ "Noto Serif" ];
      };
    };
  };
}
