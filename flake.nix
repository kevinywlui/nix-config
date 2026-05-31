{
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-25.11";
    nixpkgs-unstable.url = "github:nixos/nixpkgs/nixos-unstable";
    nixos-hardware.url = "github:NixOS/nixos-hardware/master";
    disko.url = "github:nix-community/disko";
    disko.inputs.nixpkgs.follows = "nixpkgs";

    home-manager.url = "github:nix-community/home-manager/release-25.11";
    home-manager.inputs.nixpkgs.follows = "nixpkgs";

    sops-nix.url = "github:Mic92/sops-nix";
    sops-nix.inputs.nixpkgs.follows = "nixpkgs-unstable";
  };

  outputs = { self, nixpkgs, nixos-hardware, disko, home-manager, ... }@inputs:
    let
      # dotfilesPath is the flake source (a read-only /nix/store path during
      # evaluation). Consumers that need to *write* to the working tree
      # (setup-dotfiles) inline ~/Code/dotfiles directly — see profiles/core.nix.
      dotfilesPath = self.outPath;

      nixpkgsConfig = {
        nixpkgs.overlays = [ (import ./nix/overlays inputs) ];
        nixpkgs.config.allowUnfree = true;
      };

      mkHost = { hwModule, hostModule }: nixpkgs.lib.nixosSystem {
        system = "x86_64-linux";
        specialArgs = { inherit inputs dotfilesPath; };
        modules = [
          nixpkgsConfig
          hwModule
          disko.nixosModules.disko
          home-manager.nixosModules.home-manager
          inputs.sops-nix.nixosModules.sops
          ./nix/hosts/common/disko.nix
          hostModule
        ];
      };

      # Individual NixOS modules — one attr per importable unit, plus a
      # `default` that bundles them. Local let-binding avoids a fixed-point
      # ref to `self.nixosModules` inside the `default` aggregator.
      nixosModulesAttrs = {
        core = ./nix/modules/nixos/profiles/core.nix;
        desktop = ./nix/modules/nixos/profiles/desktop.nix;
        dev = ./nix/modules/nixos/profiles/dev.nix;
        laptop-hardware = ./nix/modules/nixos/profiles/laptop-hardware.nix;
        paperless = ./nix/modules/nixos/services/paperless.nix;
        status-page = ./nix/modules/nixos/services/status-page.nix;
        ports = ./nix/modules/nixos/ports.nix;
      };

      homeManagerModulesAttrs = {
        core = ./nix/modules/home/profiles/core.nix;
        desktop = ./nix/modules/home/profiles/desktop.nix;
        nightlight = ./nix/modules/home/services/nightlight;
      };
    in
    {
      nixosConfigurations.fw13 = mkHost {
        hwModule = nixos-hardware.nixosModules.framework-intel-core-ultra-series1;
        hostModule = ./nix/hosts/fw13;
      };

      nixosConfigurations.t480 = mkHost {
        hwModule = nixos-hardware.nixosModules.lenovo-thinkpad-t480;
        hostModule = ./nix/hosts/t480;
      };

      nixosModules = nixosModulesAttrs // {
        default = { imports = builtins.attrValues nixosModulesAttrs; };
      };

      homeManagerModules = homeManagerModulesAttrs // {
        default = { imports = builtins.attrValues homeManagerModulesAttrs; };
      };

      formatter.x86_64-linux = nixpkgs.legacyPackages.x86_64-linux.nixpkgs-fmt;
    };
}
