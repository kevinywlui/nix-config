# base/ — portable dotfiles (GNU Stow)

Two Stow packages, shared verbatim between NixOS and other Linux distributions:

- **`core/`** — runs without a graphical session: shell, git, editor,
  `~/.local/bin/battery`. Safe on a headless box.
- **`desktop/`** — graphical session helpers. Requires at least PipeWire +
  WirePlumber for the audio scripts (`audio-switch.sh`, `eww-audio.sh`); the
  eww/sway/hypr/kanshi/dunst/rofi/kitty configs additionally require a
  wlroots Wayland compositor.

These map onto the Makefile: `make install-headless` stows `core` only; `make install`
stows both. On NixOS the *same* files are symlinked by Home Manager
(`mkOutOfStoreSymlink`) rather than copied, so edits here take effect in new sessions
immediately — **no rebuild required**.

## Portability boundary

The dotfiles and `~/.local/bin` scripts run on any Linux — launched manually or by your
compositor's `exec`/keybindings. What is **NixOS-only** is the *service wiring* in
`../nix/modules/home/`: those systemd user units hardcode `/run/current-system/sw/bin`
and depend on generated config files (e.g. `~/.config/nightlight/config`). **Stow
installs files, not units** — a non-NixOS user gets the configs but must wire any
timers themselves and provide the generated config.

A related caveat: the NixOS units lean on `graphical-session.target`, which is set up
by the NixOS+sway integration. Stowing the sway config alone does not start that
target; non-NixOS users either start an equivalent target themselves or run their
session manager's autostart equivalent.

If a target file already exists, `make adopt` pulls it into the repo before symlinking;
otherwise stow aborts on the conflict.

**Rule:** keep NixOS-exclusive logic out of `base/`. See `../AGENTS.md` → *Cross-Platform
Configuration Strategy* for the rationale.
