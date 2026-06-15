{ pkgs, lib, inputs, ... }:

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
  systemd.targets.network.wantedBy = [ "multi-user.target" ];

  services.fprintd.enable = true;
  security.pam.services.login.fprintAuth = true;
  security.pam.services.sudo.fprintAuth = true;

  services.logind.settings.Login.HandleLidSwitch = "suspend";

  boot.loader.systemd-boot.enable = true;
  boot.loader.systemd-boot.consoleMode = "max";
  boot.loader.systemd-boot.configurationLimit = 10;
  boot.loader.efi.canTouchEfiVariables = true;
  boot.loader.timeout = 1;
  boot.kernelParams = [
    "nvme.noacpi=1" # workaround: NVMe ACPI power management conflicts with FW13 Intel Core Ultra, causes boot failures
    "rng_core.default_quality=0" # stop hwrng from polling tpm-rng-0; entropy pool is kept full by RDRAND/RDSEED
    "nmi_watchdog=0" # reduce CPU wakeups; NMI watchdog is only useful in server/debug contexts
    "i915.enable_psr=1" # Panel Self Refresh
    "i915.enable_fbc=1" # Framebuffer Compression (~0.4W savings; risk: rare screen flickering)
    "i915.enable_guc=3" # GuC/HuC firmware for GPU scheduling and power management
  ];
  # 60s lets the CPU reach C10 during silence; short enough to avoid pop/click at session start.
  boot.extraModprobeConfig = "options snd_hda_intel power_save=60";
  # Reduce writeback wakeups by stretching the flush interval from 5s to 15s.
  boot.kernel.sysctl."vm.dirty_writeback_centisecs" = 1500;
  networking.interfaces.wlp170s0.wakeOnLan.enable = false;

  services.tlp.settings = {
    # Goodix fingerprint reader (27c6:609c): autosuspend causes fprintd to miss the first auth attempt.
    USB_EXCLUDE_DEVICES = "27c6:609c";
    # HDMI Expansion Card (32ac:0002): force autosuspend to stop the ~1W idle drain.
    USB_ALLOWLIST = "32ac:0002";
    # laptop-hardware.nix sets SOUND_POWER_SAVE_ON_AC=0, which would undo the snd_hda_intel power_save=60
    # above on AC. Force 60s on both to keep C10 reachable and avoid pop/click at 1s (TLP's BAT default).
    SOUND_POWER_SAVE_ON_AC = lib.mkForce 60;
    SOUND_POWER_SAVE_ON_BAT = lib.mkForce 60;
  };

  services.udev.packages = [ pkgs.platformio-core ];
  services.udev.extraRules = ''
    # wakeOnLan only covers the network stack; this disables PCI-level wakeup for the AX210
    # (8086:2725) so the card can enter D3cold and the CPU can reach C10.
    ACTION=="add", SUBSYSTEM=="pci", ATTR{vendor}=="0x8086", ATTR{device}=="0x2725", ATTR{power/wakeup}="disabled"
    SUBSYSTEMS=="usb", ATTRS{idVendor}=="303a", ATTRS{idProduct}=="1001", GROUP="dialout", MODE="0660"
  '';
  users.users.klui.extraGroups = [ "dialout" ];

  # "steady" uses a 60s moving average and a flat 15% zone up to 60°C (idle is ~45–55°C),
  # which stops the audible fan cycling caused by "agile"'s 15s average reacting to small temp swings.
  # "agile" is retained so `fw-fanctrl use agile` works at runtime.
  environment.etc."fw-fanctrl/config.json".text = builtins.toJSON {
    "\$schema" = "./config.schema.json";
    defaultStrategy = "steady";
    strategyOnDischarging = "";
    strategies = {
      steady = {
        fanSpeedUpdateFrequency = 5;
        movingAverageInterval = 60;
        speedCurve = [
          { temp = 0; speed = 15; }
          { temp = 60; speed = 15; }
          { temp = 70; speed = 40; }
          { temp = 90; speed = 100; }
        ];
      };
      agile = {
        fanSpeedUpdateFrequency = 3;
        movingAverageInterval = 15;
        speedCurve = [
          { temp = 0; speed = 15; }
          { temp = 40; speed = 15; }
          { temp = 60; speed = 30; }
          { temp = 70; speed = 40; }
          { temp = 75; speed = 80; }
          { temp = 85; speed = 100; }
        ];
      };
    };
  };

  systemd.services.fw-fanctrl = {
    description = "Framework Fan Controller";
    wantedBy = [ "multi-user.target" ];
    after = [ "multi-user.target" ];
    path = [ pkgs.fw-ectool ];
    serviceConfig = {
      ExecStart = "${pkgs.fw-fanctrl}/bin/fw-fanctrl run --silent steady";
      Restart = "on-failure";
      RestartSec = "5s";
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
  };

  environment.systemPackages = with pkgs; [ fw-fanctrl ];

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
  };
}
