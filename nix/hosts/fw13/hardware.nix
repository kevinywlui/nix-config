{ lib, ... }:

{
  hardware.enableRedistributableFirmware = true;

  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";

  # boot.initrd.systemd.enable is set for all hosts in modules/nixos/profiles/core.nix.

  # Boot-critical kernel modules (nvme, xhci_pci, etc.) are supplied by
  # nixos-hardware.nixosModules.framework-amd-ai-300-series in flake.nix.
}
