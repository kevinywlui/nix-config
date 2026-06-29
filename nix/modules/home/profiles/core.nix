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
    Unit.Description = "Rebuild flat by-title copy of Calibre library and add missing epub TOCs";
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

        # Add a table of contents to any epub in the flat view that lacks one,
        # so the e-readers (Boox, Pixel) get chapter navigation even when the
        # source file shipped without it. epub-toc is idempotent — files that
        # already have a TOC are left untouched — so re-running on every library
        # change is cheap. We only touch the flat BOOKS copies here, never the
        # Calibre-managed originals under "$CALIBRE".
        ${pkgs.epub-toc}/bin/epub-toc --quiet "$BOOKS" || true
      '';
    };
  };

  systemd.user.paths.calibre-books-view = {
    Unit.Description = "Watch Calibre library for changes";
    Path.PathChanged = "/var/lib/syncthing/calibre";
    Install.WantedBy = [ "default.target" ];
  };

  # Weekly read-only audit of which CLI tools to declare vs drop. Writes a
  # markdown report (INSTALL candidates from ad-hoc nix-shell pulls, REMOVE
  # candidates from declared-but-unused tools) to ~/.local/state; never edits
  # anything. The script lives in the live tree, so it tracks edits without a
  # rebuild — run it on demand with `nix run .#tool-report`. dotfilesPath is a
  # literal string here, so this interpolates a path, not a store import.
  systemd.user.services.tool-usage-report = {
    Unit.Description = "Generate CLI tool usage report (install/remove candidates)";
    Service = {
      Type = "oneshot";
      ExecStart = pkgs.writeShellScript "tool-usage-report" ''
        set -euo pipefail
        ${pkgs.coreutils}/bin/mkdir -p "$HOME/.local/state"
        ${pkgs.python3}/bin/python3 \
          ${dotfilesPath}/nix/scripts/tool-usage-report.py \
          > "$HOME/.local/state/tool-usage-report.md"
      '';
    };
  };

  systemd.user.timers.tool-usage-report = {
    Unit.Description = "Weekly CLI tool usage report";
    Timer = {
      OnCalendar = "weekly";
      # Laptops are often off at the scheduled time; catch up on next boot.
      Persistent = true;
    };
    Install.WantedBy = [ "timers.target" ];
  };

  home.stateVersion = "24.11";
}
