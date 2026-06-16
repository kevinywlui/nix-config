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
  unstable = import inputs.nixpkgs-unstable {
    system = final.stdenv.hostPlatform.system;
    config.allowUnfree = true;
  };
} // (import ./gtd.nix inputs final prev)
