{ pkgs, ... }:

let
  # Weekly desktop nudge when fwupd has stable firmware updates. Reads the
  # metadata that fwupd-refresh.timer already keeps fresh (a pure query, no
  # polkit/sudo), so it runs as the user and notify-send reaches dunst on the
  # user session bus. Applying updates still needs `fwupdmgr update` + reboot.
  fwupdNotify = pkgs.writeShellApplication {
    name = "fwupd-update-notify";
    runtimeInputs = with pkgs; [ fwupd jq libnotify ];
    text = ''
      json=$(fwupdmgr get-updates --json 2>/dev/null || true)
      count=$(printf '%s' "$json" | jq -r '(.Devices // []) | length' 2>/dev/null || echo 0)
      [ "''${count:-0}" -gt 0 ] || exit 0
      names=$(printf '%s' "$json" | jq -r '[.Devices[].Name] | unique | join(", ")')
      notify-send \
        --app-name=fwupd \
        --icon=software-update-available \
        --urgency=normal \
        "Firmware updates available" \
        "$names — run 'fwupdmgr update' (needs sudo + reboot)"
    '';
  };
in
{
  imports = [
    ../../modules/nixos/profiles/core.nix
    ../../modules/nixos/profiles/dev.nix
    ../../modules/nixos/profiles/agent-tools.nix
    ../../modules/nixos/profiles/desktop.nix
    ../../modules/nixos/profiles/laptop-hardware.nix
    ./hardware.nix
  ];

  networking.hostName = "fw13";
  sops.defaultSopsFile = ./secrets.yaml;

  networking.networkmanager.enable = true;
  networking.networkmanager.dns = "systemd-resolved";
  # NetworkManager-wait-online blocks boot until a connection is established,
  # which is too slow and unnecessary for interactive laptop use
  systemd.services.NetworkManager-wait-online.enable = false;

  # nixos-hardware already defaults this on; kept as intent documentation.
  # PAM fprintAuth (login/sudo/greetd) follows this option automatically.
  services.fprintd.enable = true;

  services.logind.settings.Login.HandleLidSwitch = "suspend";

  boot.loader.systemd-boot.enable = true;
  boot.loader.systemd-boot.consoleMode = "max";
  boot.loader.systemd-boot.configurationLimit = 10;
  boot.loader.efi.canTouchEfiVariables = true;
  boot.loader.timeout = 1;
  # GPU params (amdgpu in initrd, PSR-hang workaround) come from
  # nixos-hardware's framework-amd-ai-300-series module. Shared power tweaks
  # (nmi_watchdog=0, writeback interval, snd_hda_intel power_save) live in
  # profiles/laptop-hardware.nix.

  # nixos-hardware's AMD module defaults power-profiles-daemon on, but nixpkgs
  # asserts PPD and TLP are mutually exclusive. TLP wins here because it carries
  # the shared AC/BAT policy suite in profiles/laptop-hardware.nix (EPP, platform
  # profile, PCIe ASPM, runtime PM), which PPD does not replicate.
  services.power-profiles-daemon.enable = false;

  services.tlp.settings = {
    # Goodix fingerprint reader (27c6:609c): autosuspend causes fprintd to miss the first auth attempt.
    USB_DENYLIST = "27c6:609c";
    # HDMI Expansion Card (32ac:0002): force autosuspend to stop the ~1W idle drain.
    USB_ALLOWLIST = "32ac:0002";
  };

  services.udev.packages = [ pkgs.platformio-core.udev ];
  services.udev.extraRules = ''
    # Disable PCI-level wakeup for the AX210 (8086:2725) so the card can enter D3cold at idle.
    ACTION=="add", SUBSYSTEM=="pci", ATTR{vendor}=="0x8086", ATTR{device}=="0x2725", ATTR{power/wakeup}="disabled"
    SUBSYSTEMS=="usb", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", GROUP="dialout", MODE="0660"
  '';
  users.users.klui.extraGroups = [ "dialout" ];

  # "steady" uses a 60s moving average and a flat 15% zone up to 60°C (idle is ~45–55°C),
  # which stops the audible fan cycling caused by "agile"'s 15s average reacting to small temp swings.
  # The module merges this over the package's default config, so the stock
  # strategies (including "agile") stay available for `fw-fanctrl use` at runtime.
  # It also installs fw-fanctrl/fw-ectool, restores EC auto fan control on
  # service stop (ExecStopPost autofanctrl), and adds the suspend/resume hook.
  hardware.fw-fanctrl = {
    enable = true;
    config = {
      defaultStrategy = "steady";
      strategies.steady = {
        fanSpeedUpdateFrequency = 5;
        movingAverageInterval = 60;
        speedCurve = [
          { temp = 0; speed = 15; }
          { temp = 60; speed = 15; }
          { temp = 70; speed = 40; }
          { temp = 90; speed = 100; }
        ];
      };
    };
  };

  # Hardening overlay on the upstream unit (the module only sets
  # ExecStart/Restart/ExecStopPost, so these merge cleanly).
  systemd.services.fw-fanctrl.serviceConfig = {
    NoNewPrivileges = true;
    PrivateTmp = true;
    ProtectHome = true;
    ProtectClock = true;
    ProtectHostname = true;
    ProtectKernelLogs = true;
    ProtectKernelModules = true;
    ProtectControlGroups = true;
    RestrictNamespaces = true;
    LockPersonality = true;
  };

  system.stateVersion = "24.11";

  home-manager.users.klui = {
    imports = [
      ../../modules/home/profiles/core.nix
      ../../modules/home/profiles/desktop.nix
    ];
    home.packages = with pkgs; [
      # GUI dev IDEs + physical-flashing tools live per-host, kept out of
      # profiles/dev.nix so the headless t480 imports that profile without them.
      # (The shared CLI build tools — arduino-cli, gradle, etc. — are in dev.nix.)
      android-studio
      arduino-ide
      esptool
      platformio
    ];

    systemd.user.services.fwupd-update-notify = {
      Unit.Description = "Notify if firmware (fwupd) updates are available";
      Service = {
        Type = "oneshot";
        ExecStart = "${fwupdNotify}/bin/fwupd-update-notify";
      };
    };
    systemd.user.timers.fwupd-update-notify = {
      Unit.Description = "Weekly firmware update check";
      Timer = {
        OnCalendar = "weekly";
        Persistent = true;
        RandomizedDelaySec = "1h";
      };
      Install.WantedBy = [ "timers.target" ];
    };
  };
}
