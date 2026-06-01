{ pkgs, inputs, config, dotfilesPath, ... }:

let
  # setup-dotfiles clones into $HOME at activation time and needs a shell-
  # expandable literal (Nix string interpolation can't produce $HOME). Keep
  # this in sync with `dotfilesPath` in ../../../../flake.nix if you relocate
  # the working tree.
  cloneTarget = "$HOME/Code/dotfiles"; # CHANGE-ME if relocating the working tree
  setupDotfiles = pkgs.writeShellApplication {
    name = "setup-dotfiles";
    runtimeInputs = with pkgs; [ git gnumake coreutils ];
    text = ''
      mkdir -p "$HOME/Code"

      if [ "''${1:-}" == "--force" ]; then
        read -r -p "This will permanently delete ${cloneTarget}. Are you sure? [y/N] " confirm
        if [ "$confirm" != "y" ] && [ "$confirm" != "Y" ]; then
          echo "Aborted."
          exit 1
        fi
        rm -rf "${cloneTarget}"
      fi

      if [ -d "${cloneTarget}" ]; then
        echo "Dotfiles already exist at ${cloneTarget}. Use --force to overwrite."
      else
        echo "Cloning dotfiles..."
        git clone https://github.com/kevinywlui/dotfiles.git "${cloneTarget}"

        echo "Installing zplug..."
        cd "${cloneTarget}"
        make zplug
      fi
    '';
  };
in
{
  imports = [ ./system-label.nix ];

  nix.settings = {
    experimental-features = [ "nix-command" "flakes" ];
    auto-optimise-store = true;
  };
  nix.registry.nixpkgs.flake = inputs.nixpkgs;

  # nixpkgs.overlays and nixpkgs.config.allowUnfree are wired at the flake
  # layer (../../../../flake.nix → nixpkgsConfig) so every host gets them
  # whether or not it imports this profile.

  home-manager.useGlobalPkgs = true;
  home-manager.useUserPackages = true;
  home-manager.backupFileExtension = "backup";
  home-manager.extraSpecialArgs = { inherit inputs dotfilesPath; };

  programs.nh = {
    enable = true;
    clean.enable = true;
    clean.extraArgs = "--keep-since 14d --keep 5";
    # `path:` prefix is load-bearing: a bare absolute path inside a git
    # repo is auto-promoted by Nix to `git+file:`, which only sees committed
    # HEAD (breaking the build-before-commit workflow). `path:` forces
    # working-tree snapshot semantics on every eval.
    flake = "path:${dotfilesPath}";
  };

  # Keep the weekly cadence + Persistent=true catch-up, but push the catch-up
  # tick out of the login critical path and deprioritize its IO so the user
  # session doesn't share disk with an 80s, multi-GB nix-store --gc.
  systemd.timers.nh-clean.timerConfig.RandomizedDelaySec = "30min";
  systemd.services.nh-clean.serviceConfig = {
    Nice = 19;
    IOSchedulingClass = "idle";
  };

  time.timeZone = "America/Los_Angeles";

  # Enable BBR congestion control with fq qdisc for improved throughput and latency
  boot.kernel.sysctl = {
    "net.core.default_qdisc" = "fq";
    "net.ipv4.tcp_congestion_control" = "bbr";
  };

  users.users.klui = {
    isNormalUser = true;
    extraGroups = [ "wheel" "syncthing" ];
    shell = pkgs.zsh;
  };

  programs.zsh.enable = true;
  programs.git.enable = true;
  programs.htop.enable = true;
  programs.neovim = {
    enable = true;
    defaultEditor = true;
    package = pkgs.unstable.neovim-unwrapped;
  };

  environment.systemPackages =
    let
      systemUtils = with pkgs; [
        acpi
        btrfs-progs
        gnumake
        age
        pre-commit
        sops
        ssh-to-age
        stow
        unstable.tailscale

        btop
        gcc
        lm_sensors
        pciutils
        unzip
        traceroute
        usbutils
        wget
        kitty.terminfo
      ];

      cliTools = with pkgs; [
        fd
        fzf
        gh
        jj
        nushell
        ripgrep
        starship
        unstable.gemini-cli-bin
        pkgs.unstable.claude-code
        vim
        zoxide
        setupDotfiles
      ];
    in
    systemUtils ++ cliTools;

  services.tailscale = {
    enable = true;
    package = pkgs.unstable.tailscale;
  };
  services.resolved.enable = true;
  services.earlyoom.enable = true;

  services.syncthing = {
    enable = true;
    openDefaultPorts = false;
  };
  networking.firewall.interfaces.tailscale0 = {
    allowedTCPPorts = [ 22000 ];
    allowedUDPPorts = [ 22000 21027 ];
  };
  systemd.services.syncthing.serviceConfig.UMask = "0007";
  users.users.syncthing.homeMode = "0770";

  systemd.tmpfiles.rules = [
    "d /var/lib/syncthing 0770 syncthing syncthing -"
    "d /var/lib/syncthing/books 0770 syncthing syncthing -"
  ];

  services.btrfs.autoScrub = {
    enable = true;
    interval = "weekly";
    fileSystems = [ "/" ];
  };

  sops.age.sshKeyPaths = [ "/etc/ssh/ssh_host_ed25519_key" ];

  services.openssh = {
    enable = true;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
      PermitRootLogin = "no";
    };
  };

  networking.firewall.allowedTCPPorts = [ 22 ];

  zramSwap.enable = true;
}
