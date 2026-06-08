{ config, pkgs, dotfilesPath, ... }:

let
  basePath = "${dotfilesPath}/base/core";
  link = file: config.lib.file.mkOutOfStoreSymlink "${basePath}/${file}";
in
{
  home.username = "klui";
  home.homeDirectory = "/home/klui";

  home.file = {
    ".zshrc".source = link ".zshrc";
    ".gitconfig".source = link ".gitconfig";
    ".p10k.zsh".source = link ".p10k.zsh";
    ".local/bin/battery".source = link ".local/bin/battery";
    ".ssh/config".source = link ".ssh/config";
  };

  # The SSH config (above) multiplexes sessions over a control socket; ssh won't
  # create the socket's parent dir, so ensure ~/.ssh/sockets exists at 0700.
  # A real dir (not a symlink into the working tree) keeps runtime sockets out of git.
  home.activation.sshSockets = config.lib.dag.entryAfter [ "writeBoundary" ] ''
    run mkdir -p "$HOME/.ssh/sockets"
    run chmod 0700 "$HOME/.ssh/sockets"
  '';

  xdg.configFile = {
    "nvim".source = link ".config/nvim";
    # ~/.config/nightlight/config is generated in nix/modules/home/services/nightlight
    # (single source of truth shared with the nightlight systemd timers).
  };

  programs.home-manager.enable = true;

  programs.tmux = {
    enable = true;
  };

  systemd.user.services.calibre-books-view = {
    Unit.Description = "Rebuild flat by-title copy view of Calibre library";
    Service = {
      Type = "oneshot";
      ExecStartPre = "${pkgs.coreutils}/bin/sleep 10";
      ExecStart = pkgs.writeShellScript "calibre-books-view" ''
        set -euo pipefail
        export PATH="${pkgs.coreutils}/bin:${pkgs.findutils}/bin:${pkgs.gnugrep}/bin"
        CALIBRE=/var/lib/syncthing/calibre
        BOOKS=/var/lib/syncthing/books

        # Remove stale entries (symlinks or copies whose source no longer exists)
        find "$BOOKS" -maxdepth 1 \( -type f -o -type l \) \
          \( -name "*.epub" -o -name "*.pdf" -o -name "*.mobi" \
             -o -name "*.azw3" -o -name "*.cbz" \) |
        while read -r entry; do
          name=$(basename "$entry")
          if ! find "$CALIBRE" -mindepth 3 -maxdepth 3 -name "$name" -quit | grep -q .; then
            rm "$entry"
          fi
        done

        # Copy any book not already present as a regular file
        find "$CALIBRE" -mindepth 3 -maxdepth 3 -type f \
          \( -name "*.epub" -o -name "*.pdf" -o -name "*.mobi" \
             -o -name "*.azw3" -o -name "*.cbz" \) |
        while read -r book; do
          name=$(basename "$book")
          # Replace symlinks with real copies; skip if already a regular file
          if [ -L "$BOOKS/$name" ] || [ ! -e "$BOOKS/$name" ]; then
            cp "$book" "$BOOKS/$name"
            chmod 0644 "$BOOKS/$name"
          fi
        done
      '';
    };
  };

  systemd.user.paths.calibre-books-view = {
    Unit.Description = "Watch Calibre library for changes";
    Path.PathChanged = "/var/lib/syncthing/calibre";
    Install.WantedBy = [ "default.target" ];
  };

  home.stateVersion = "24.11";
}
