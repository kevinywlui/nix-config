{ pkgs, ... }:

{
  # This profile is shared across Intel and AMD hosts — vendor-specific bits
  # (early-KMS initrd modules, thermald, microcode) belong in per-host config
  # or the host's nixos-hardware module, not here.

  services.fwupd.enable = true;
  services.upower.enable = true;

  # NMI watchdog causes periodic per-CPU wakeups and is only useful in
  # server/debug contexts.
  boot.kernelParams = [ "nmi_watchdog=0" ];
  # Reduce writeback wakeups by stretching the flush interval from 5s to 15s.
  boot.kernel.sysctl."vm.dirty_writeback_centisecs" = 1500;
  # 60s lets the HDA codec runtime-suspend during silence so the package can
  # reach deep C-states at idle; long enough to avoid pop/click at session
  # start (vs TLP's 1s BAT default).
  boot.extraModprobeConfig = "options snd_hda_intel power_save=60";

  services.tlp = {
    enable = true;
    settings = {
      # "powersave" does NOT mean low-performance — on both intel_pstate and
      # amd_pstate=active it defers to EPP and platform profile for tuning.
      # "performance" would pin frequency high and defeat the EPP settings below.
      CPU_SCALING_GOVERNOR_ON_AC = "powersave";
      CPU_SCALING_GOVERNOR_ON_BAT = "powersave";

      CPU_ENERGY_PERF_POLICY_ON_AC = "balance_performance";
      CPU_ENERGY_PERF_POLICY_ON_BAT = "power";

      # Controls TDP/turbo envelope at firmware level, independent of the governor.
      PLATFORM_PROFILE_ON_AC = "performance";
      PLATFORM_PROFILE_ON_BAT = "low-power";

      # Match the snd_hda_intel power_save=60 modprobe option above on both
      # rails — TLP rewrites the sysfs knob on AC/BAT transitions, and 0 on AC
      # would keep the codec powered, pinning the package out of deep C-states.
      SOUND_POWER_SAVE_ON_AC = 60;
      SOUND_POWER_SAVE_ON_BAT = 60;
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
