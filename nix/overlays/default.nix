# Aggregates every overlay this repo applies. Takes flake inputs and returns
# a single overlay function `final: prev: { ... }` suitable for
# `nixpkgs.overlays`.
#
# Contract:
#   - This file evaluates to `inputs: final: prev: { ... }`.
#   - Wire it once in flake.nix via
#       nixpkgs.overlays = [ (import ./nix/overlays inputs) ];
#     (positional — the file's first param is `inputs`), rather than from
#     inside a module — keeps a host that skips core.nix from silently
#     losing the overlay.
#   - To add another overlay: write `nix/overlays/<name>.nix` as an
#     `inputs: final: prev: { ... }` function and merge it into the
#     attrset below with `// (import ./<name>.nix inputs final prev)`.
inputs: final: prev: {
  # Tier 3 input (see flake.nix + AGENTS.md). Exposed as pkgs.claude-desktop so
  # hosts consume it like any other package. Non-FHS variant chosen for least
  # privilege; upstream also ships `claude-desktop-fhs`, which bundles
  # docker/uv/node to host MCP servers — swap the attr below only if a concrete
  # MCP workflow needs that larger ambient toolchain.
  claude-desktop = inputs.claude-desktop.packages.${final.stdenv.hostPlatform.system}.claude-desktop;

  unstable = import inputs.nixpkgs-unstable {
    system = final.stdenv.hostPlatform.system;
    config.allowUnfree = true;
  };
}
