{ pkgs, ... }:

{
  # i915 in initrd enables early KMS
  boot.initrd.availableKernelModules = [ "i915" ];

  services.fwupd.enable = true;
  services.thermald.enable = true;
  services.upower.enable = true;

  services.tlp = {
    enable = true;
    settings = {
      # "powersave" does NOT mean low-performance — on Intel it defers to EPP
      # and platform profile for tuning. "performance" would pin frequency high
      # and defeat the EPP settings below.
      CPU_SCALING_GOVERNOR_ON_AC = "powersave";
      CPU_SCALING_GOVERNOR_ON_BAT = "powersave";

      CPU_ENERGY_PERF_POLICY_ON_AC = "balance_performance";
      CPU_ENERGY_PERF_POLICY_ON_BAT = "power";

      # Controls TDP/turbo envelope at firmware level, independent of the governor.
      PLATFORM_PROFILE_ON_AC = "performance";
      PLATFORM_PROFILE_ON_BAT = "low-power";

      SOUND_POWER_SAVE_ON_AC = 0;
      SOUND_POWER_SAVE_ON_BAT = 1;
      SOUND_POWER_SAVE_CONTROLLER = "Y";

      PCIE_ASPM_ON_AC = "default";
      PCIE_ASPM_ON_BAT = "powersupersave";

      # "auto" on AC allows PCI devices (NVMe, GPU, Thunderbolt) to suspend when
      # idle rather than staying at full power regardless of load.
      RUNTIME_PM_ON_AC = "auto";
      RUNTIME_PM_ON_BAT = "auto";

      SATA_LINKPWR_ON_AC = "med_power_with_dipm";
      SATA_LINKPWR_ON_BAT = "med_power_with_dipm";

      WIFI_PWR_ON_AC = "off";
      WIFI_PWR_ON_BAT = "on";

      USB_AUTOSUSPEND = 1;

      DEVICES_TO_DISABLE_ON_BAT_NOT_IN_USE = "bluetooth";
    };
  };

  programs.light.enable = true;

  environment.systemPackages = with pkgs; [
    powertop
  ];
}
