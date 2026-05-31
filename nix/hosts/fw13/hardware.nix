{ lib, ... }:

{
  hardware.enableRedistributableFirmware = true;

  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";

  # TODO: move to a shared module (e.g. modules/nixos/core.nix) — this should be the default for all hosts
  boot.initrd.systemd.enable = true;

  # Boot-critical kernel modules (nvme, xhci_pci, etc.) are supplied by
  # nixos-hardware.nixosModules.framework-intel-core-ultra-series1 in flake.nix.
}
