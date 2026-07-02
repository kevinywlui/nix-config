{ lib, ... }:

{
  hardware.enableRedistributableFirmware = true;

  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";

  # boot.initrd.systemd.enable is set for all hosts in modules/nixos/profiles/core.nix.

  # Boot-critical kernel modules (nvme, xhci_pci, etc.) come from nixpkgs'
  # boot.initrd.includeDefaultModules list; nixos-hardware's
  # framework-amd-ai-300-series module (wired in flake.nix) adds the
  # platform-specific bits (amdgpu in initrd, kernel params).
}
