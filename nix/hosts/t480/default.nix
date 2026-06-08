{ pkgs, inputs, config, ... }:

let
  # Declarative Android SDK for building round-earth-project's bike-computer-android
  # app on this headless server. fw13 gets its SDK from android-studio's GUI
  # manager; t480 has no GUI, so the SDK is pinned in Nix. Versions track the
  # app's compileSdk and the CI's build-tools — see bike-computer-android/
  # app/build.gradle.kts and .github/workflows/android.yml ("android-35",
  # "build-tools;35.0.0"). License acceptance is wired in flake.nix.
  androidSdk = pkgs.androidenv.composeAndroidPackages {
    platformVersions = [ "35" ];
    buildToolsVersions = [ "35.0.0" ];
  };
  androidSdkRoot = "${androidSdk.androidsdk}/libexec/android-sdk";
in
{
  imports = [
    ../../modules/nixos/profiles/core.nix
    ../../modules/nixos/profiles/dev.nix
    ../../modules/nixos/profiles/laptop-hardware.nix
    ../../modules/nixos/services/paperless.nix
    ../../modules/nixos/services/status-page.nix
    ./hardware.nix
  ];

  networking.hostName = "t480";

  sops.defaultSopsFile = ./secrets.yaml;
  sops.secrets.hc-ping-url = { };
  sops.secrets.paperless-password = { };

  boot.loader.systemd-boot.enable = true;
  boot.loader.systemd-boot.configurationLimit = 10;
  boot.loader.efi.canTouchEfiVariables = true;

  services.status-page = {
    enable = true;
    monitoredServices = [
      { name = "Paperless"; unit = "paperless-web.service"; }
    ];
  };
  # Prevent suspend when lid is closed; t480 runs headless
  services.logind.settings.Login.HandleLidSwitch = "ignore";

  services.tlp = {
    settings = {
      # Battery charge thresholds for longevity (Always plugged in)
      # 50-60% is chemically ideal for sitting on AC power 24/7.
      START_CHARGE_THRESH_BAT0 = 50;
      STOP_CHARGE_THRESH_BAT0 = 60;
      START_CHARGE_THRESH_BAT1 = 50;
      STOP_CHARGE_THRESH_BAT1 = 60;
    };
  };

  systemd.services.hc-ping = {
    description = "Health Check Ping";
    after = [ "network-online.target" ];
    wants = [ "network-online.target" ];
    serviceConfig = {
      Type = "oneshot";
      ExecStart = toString (pkgs.writeShellScript "hc-ping" ''
        URL=$(cat ${config.sops.secrets.hc-ping-url.path})
        exec ${pkgs.curl}/bin/curl -fsS -m 10 --retry 5 -o /dev/null "$URL"
      '');
      NoNewPrivileges = true;
      ProtectSystem = "strict";
      ProtectHome = true;
      PrivateTmp = true;
    };
  };

  systemd.timers.hc-ping = {
    description = "Hourly Health Check Ping";
    wantedBy = [ "timers.target" ];
    timerConfig = {
      OnCalendar = "hourly";
      Persistent = true;
      Unit = "hc-ping.service";
    };
  };

  users.users.klui.openssh.authorizedKeys.keys = [
    "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHV+hk6kAxr4MtnjUdIeW5aBTwcYnOYKAt/psLyHb3q6 klui@fw13"
  ];

  system.stateVersion = "24.11";

  home-manager.users.klui = {
    imports = [
      ../../modules/home/profiles/core.nix
      # Headless: no desktop profile.
    ];
    # Point Gradle's Android plugin at the pinned SDK. With no sdk.dir in the
    # (gitignored) local.properties on this host, AGP falls back to these env
    # vars. ANDROID_HOME is AGP's canonical var; ANDROID_SDK_ROOT is set too for
    # older tooling. arduino-cli (the .ino build) comes from profiles/dev.nix.
    home.sessionVariables = {
      ANDROID_HOME = androidSdkRoot;
      ANDROID_SDK_ROOT = androidSdkRoot;
    };
  };
}
