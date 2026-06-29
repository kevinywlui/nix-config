{ pkgs, inputs, dotfilesPath, ... }:

let
  # setup-dotfiles clones into $HOME at activation time and needs a shell-
  # expandable literal (Nix string interpolation can't produce $HOME). Keep
  # this in sync with `dotfilesPath` in ../../../../flake.nix if you relocate
  # the working tree.
  cloneTarget = "$HOME/Code/nix-config"; # CHANGE-ME if relocating the working tree
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
        git clone https://github.com/kevinywlui/nix-config.git "${cloneTarget}"

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
    # Bare absolute path: Nix auto-promotes it to `git+file:`, which is
    # git-aware, so `inputs.self.rev`/`dirtyRev` are populated and the system
    # label carries a real commit hash instead of `-dirty` (see
    # profiles/system-label.nix). Tradeoff: `git+file:` copies tracked files
    # (committed or modified) but NOT untracked ones — a brand-new `.nix` file
    # must be `git add`-ed before `nh os build` will see it.
    flake = dotfilesPath;
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

  # systemd-based stage-1 init (initrd). Default for all hosts.
  boot.initrd.systemd.enable = true;

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
        epub-toc
        fd
        fzf
        gh
        gtd
        jj
        jq
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
    # Declarative topology, shared by both hosts. overrideDevices/overrideFolders
    # default true, so Nix is the source of truth: devices/folders not listed here
    # are reconciled away on rebuild. Device IDs are public key fingerprints, safe
    # to commit. Each host ignores the device entry matching its own ID.
    settings = {
      devices = {
        fw13.id = "KJZRSX3-NQGRAPW-VLWLH56-2QPI5YL-PIU74A6-5CI3WMT-W73OQXH-7FNT6AA";
        t480.id = "HIYFUBV-VS6HDGP-XXCYS7O-5POSGVP-XFF34WJ-2P3OFWI-ZJ7K4WL-KAQYKAN";
        boox.id = "TDJTJLV-EE64CEW-A7US4PS-DNA7T7E-TO7RUFJ-L7TDLCR-5NKTTKV-4QZCZQ3";
        pixel9.id = "UKAORC7-Y6W2VPY-5BJJ4GN-CCXPJJS-2BGVGLT-CIVWCZ2-72QR32F-MB5BUAG";
      };
      folders = {
        # `id` is pinned to the existing Syncthing folder IDs — these must match
        # across all devices or syncing with boox/pixel9 silently breaks. Do not
        # let the attribute name become the ID.
        calibre = {
          id = "h6hfd-nwrb7";
          path = "/var/lib/syncthing/calibre";
          devices = [ "fw13" "t480" "boox" "pixel9" ];
        };
        books = {
          id = "vjsxd-7rtse";
          path = "/var/lib/syncthing/books";
          devices = [ "fw13" "t480" "boox" "pixel9" ];
        };
      };
    };
  };
  networking.firewall.interfaces.tailscale0 = {
    # 22 = SSH, scoped to the tailnet only (mirrors the key-only openssh config
    # below) so it is not exposed on the LAN/Wi-Fi the laptops roam onto; auth is
    # already key-only, so this closes scan/CVE surface, not a break. Recovery if
    # tailscale is down: physical console (both hosts have a screen).
    # 22000/21027 = syncthing.
    allowedTCPPorts = [ 22 22000 ];
    allowedUDPPorts = [ 22000 21027 ];
  };
  systemd.services.syncthing.serviceConfig.UMask = "0007";
  users.users.syncthing.homeMode = "0770";

  systemd.tmpfiles.rules = [
    "d /var/lib/syncthing 0770 syncthing syncthing -"
    "d /var/lib/syncthing/books 0770 syncthing syncthing -"
    "d /var/lib/syncthing/calibre 0770 syncthing syncthing -"
  ];

  services.btrfs.autoScrub = {
    enable = true;
    interval = "weekly";
    fileSystems = [ "/" ];
  };

  sops.age.sshKeyPaths = [ "/etc/ssh/ssh_host_ed25519_key" ];

  services.openssh = {
    enable = true;
    # Don't let openssh punch port 22 into the firewall on all interfaces
    # (its default); SSH is scoped to tailscale0 in the firewall block above.
    openFirewall = false;
    settings = {
      PasswordAuthentication = false;
      KbdInteractiveAuthentication = false;
      PermitRootLogin = "no";
    };
  };

  zramSwap.enable = true;
}
